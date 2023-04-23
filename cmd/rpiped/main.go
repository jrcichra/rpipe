package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"time"

	"net/http/pprof"

	"github.com/oklog/run"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Args struct {
	Daemon      bool
	Addr        string
	MetricsAddr string
	HTTPTimeout time.Duration // mainly used for testing connection breakages
}

type Job struct {
	CommandHandler *exec.Cmd
	Stdin          io.WriteCloser
	Stdout         io.ReadCloser
	StdErr         io.ReadCloser
	Lock           sync.Mutex
	Name           string
}

func (j *Job) StartAndPrintOutput() error {
	if err := j.CommandHandler.Start(); err != nil {
		return err
	}
	// handle stdout until it's done
	go func() {
		scanner := bufio.NewScanner(j.Stdout)
		for scanner.Scan() {
			log.Printf("job %s stdout: %s", j.Name, scanner.Text())
		}
	}()
	// handle stderr until it's done
	go func() {
		scanner := bufio.NewScanner(j.StdErr)
		for scanner.Scan() {
			log.Printf("job %s stderr: %s", j.Name, scanner.Text())
		}
	}()
	return nil
}

type Server struct {
	Jobs map[string]*Job
}

func NewServer() *Server {
	return &Server{
		Jobs: make(map[string]*Job),
	}
}

func (s *Server) done(w http.ResponseWriter, r *http.Request) {
	jobName := r.Header.Get("Job")
	job, ok := s.Jobs[jobName]
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		msg := "cannot call done on job which doesnt exist"
		log.Println(msg)
		w.Write([]byte(msg))
		return
	}
	// copy is done, close pipe
	if err := job.Stdin.Close(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Printf("sent error 5:  %v", err)
		w.Write([]byte(err.Error()))
		return
	}

	// wait for the job to finish
	if err := job.CommandHandler.Wait(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Printf("sent error 6: %v", err)
		w.Write([]byte(err.Error()))
		return
	}

	// delete it from the map
	delete(s.Jobs, jobName)

	// successful done
	w.WriteHeader(http.StatusOK)
	log.Printf("finished processing job %s", jobName)
	w.Write([]byte("ok"))
}

func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	// make sure it's a post
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// get the job and command
	jobName := r.Header.Get("Job")
	command := r.Header.Get("Command")
	chunkSize, err := strconv.Atoi(r.Header.Get("Chunk-Size"))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Printf("sent error stdin: %v", err)
		w.Write([]byte(err.Error()))
		return
	}

	job, ok := s.Jobs[jobName]
	// have we seen this job before?
	// no - we need to do some prep-work
	if !ok {
		splitCommand := strings.Split(command, " ")
		// build a new job
		job = &Job{
			Name:           jobName,
			CommandHandler: exec.Command(splitCommand[0], splitCommand[1:]...),
		}
		s.Jobs[jobName] = job

		// grab stdin
		var err error
		job.Stdin, err = job.CommandHandler.StdinPipe()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			log.Printf("sent error stdin: %v", err)
			w.Write([]byte(err.Error()))
			return
		}
		// grab stdout
		job.Stdout, err = job.CommandHandler.StdoutPipe()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			log.Printf("sent error stdout: %v", err)
			w.Write([]byte(err.Error()))
			return
		}

		// grab stderr
		job.StdErr, err = job.CommandHandler.StderrPipe()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			log.Printf("sent error stdout: %v", err)
			w.Write([]byte(err.Error()))
			return
		}

		// start the job
		log.Printf("starting job %+v...", job)
		if err := job.StartAndPrintOutput(); nil != err {
			w.WriteHeader(http.StatusInternalServerError)
			log.Printf("sent error 2: %v", err)
			w.Write([]byte(err.Error()))
			return
		}
	}

	// lock the job so only one request can interact with this job at a time
	job.Lock.Lock()
	defer job.Lock.Unlock()

	// log.Printf("processing chunk for job %+v...", job)

	// copy data to temporary buffer before sending to the application (in case the http request fails midway through)
	buffer := bytes.NewBuffer(make([]byte, 0, chunkSize))
	b, err := io.Copy(buffer, r.Body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Printf("sent error 3: %d: %v", b, err)
		w.Write([]byte(err.Error()))
		return
	}

	if b <= 0 {
		w.WriteHeader(http.StatusInternalServerError)
		log.Printf("sent error 4: %d: %s", b, "no body found")
		w.Write([]byte("no body found"))
		return
	}

	if b != int64(chunkSize) {
		w.WriteHeader(http.StatusInternalServerError)
		log.Printf("sent error 5: unexpected number of bytes. expected: %d, got: %d", chunkSize, b)
		w.Write([]byte("unexpected number of bytes"))
		return
	}

	// pass the data to the program
	b, err = io.Copy(job.Stdin, buffer)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Printf("sent error 6: %d: %v", b, err)
		w.Write([]byte(err.Error()))
		return
	}
	if b <= 0 || b != int64(chunkSize) {
		w.WriteHeader(http.StatusInternalServerError)
		log.Printf("sent error 7: could not send all bytes from buffer to job stdin. chunkSize: %d, b: %d", chunkSize, b)
		w.Write([]byte("could not send all bytes from buffer to job stdin"))
		return
	}

	// successful chunk
	w.WriteHeader(http.StatusOK)
	log.Printf("finished processing chunk for job %s. size: %d", jobName, b)
	w.Write([]byte("ok"))

}

// RegisterDebugHandlers registers debug handlers with the mux
func RegisterDebugHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
}

func main() {
	var args Args
	flag.BoolVar(&args.Daemon, "daemon", false, "run as daemon executing commands")
	flag.StringVar(&args.Addr, "bind", ":8000", "bind addr for rpipe jobs")
	flag.StringVar(&args.MetricsAddr, "metrics", ":2100", "bind addr for metrics")
	flag.DurationVar(&args.HTTPTimeout, "timeout", 0, "http connection timeout (used primarily for testing, default = none)")
	flag.Parse()

	var g run.Group

	// signal handler
	g.Add(func() error {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		<-c
		return fmt.Errorf("interrupt signal caught")
	}, func(err error) {
	})

	{
		s := NewServer()
		mux := http.NewServeMux()
		mux.HandleFunc("/upload", s.upload)
		mux.HandleFunc("/done", s.done)

		ln, err := net.Listen("tcp", args.Addr)
		if err != nil {
			log.Fatalln(err)
		}
		g.Add(func() error {
			log.Printf("listening on %s...\n", args.Addr)
			server := http.Server{
				ReadTimeout:       args.HTTPTimeout,
				WriteTimeout:      args.HTTPTimeout,
				IdleTimeout:       args.HTTPTimeout,
				ReadHeaderTimeout: args.HTTPTimeout,
				Handler:           mux,
			}
			return server.Serve(ln)
		}, func(err error) {
			ln.Close()
		})
	}

	{
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		RegisterDebugHandlers(mux)

		ln, err := net.Listen("tcp", args.MetricsAddr)
		if err != nil {
			log.Fatalln(err)
		}
		g.Add(func() error {
			log.Printf("listening for metrics and debug on %s...\n", args.MetricsAddr)
			return http.Serve(ln, mux)
		}, func(err error) {
			ln.Close()
		})
	}

	// run the daemons
	if err := g.Run(); err != nil {
		log.Println(err)
	}
}
