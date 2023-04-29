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

	"github.com/google/uuid"
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
	BytesProcessed uint64
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
	JobsLock sync.RWMutex
	Jobs     map[string]*Job
}

func NewServer() *Server {
	return &Server{
		Jobs:     make(map[string]*Job),
		JobsLock: sync.RWMutex{},
	}
}

func (s *Server) new(w http.ResponseWriter, r *http.Request) {
	// make sure it's a post
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	// make sure command was specified
	command := r.Header.Get("Command")
	if strings.TrimSpace(command) == "" {
		w.WriteHeader(http.StatusBadRequest)
		log.Printf("no command was specified")
		if _, err := w.Write([]byte("no command was specified")); err != nil {
			log.Printf("unable to write error for no command was specified")
		}
		return
	}
	// generate a new job id
	jobName := uuid.New().String()
	// add to the list of jobs
	// build a new job
	job := &Job{
		Name:           jobName,
		CommandHandler: exec.Command("sh", "-c", command),
		Lock:           sync.Mutex{},
	}

	// add job to the map
	s.JobsLock.Lock()
	s.Jobs[jobName] = job
	s.JobsLock.Unlock()

	// grab stdin
	var err error
	job.Stdin, err = job.CommandHandler.StdinPipe()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Printf("sent error stdin: %v", err)
		if _, err := w.Write([]byte(err.Error())); err != nil {
			log.Printf("could not send error for sent error stdin: %v", err)
		}
		return
	}

	// grab stdout
	job.Stdout, err = job.CommandHandler.StdoutPipe()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Printf("sent error stdout: %v", err)
		if _, err := w.Write([]byte(err.Error())); err != nil {
			log.Printf("could not send error for sent error stdout: %v", err)
		}
		return
	}

	// grab stderr
	job.StdErr, err = job.CommandHandler.StderrPipe()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Printf("sent error stderr: %v", err)
		if _, err := w.Write([]byte(err.Error())); err != nil {
			log.Printf("could not send error for sent error stderr: %v", err)
		}
		return
	}

	// start the job
	log.Printf("starting job %+v...", job)
	if err := job.StartAndPrintOutput(); nil != err {
		w.WriteHeader(http.StatusInternalServerError)
		log.Printf("sent error 2: %v", err)
		if _, err := w.Write([]byte(err.Error())); err != nil {
			log.Printf("could not send error for sent error 2: %v", err)
		}
		return
	}

	w.WriteHeader(http.StatusOK)
	log.Printf("created new job %s with command %s", jobName, command)
	if _, err := w.Write([]byte(jobName)); err != nil {
		log.Printf("%s - %v", "failed sending back created new job", err)
	}
}

func (s *Server) resume(w http.ResponseWriter, r *http.Request) {
	// make sure it's a get
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	jobName := r.Header.Get("Job")

	s.JobsLock.RLock()
	job, ok := s.Jobs[jobName]
	s.JobsLock.RUnlock()

	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		msg := "cannot call resume on job which doesn't exist"
		log.Println(msg)
		if _, err := w.Write([]byte(msg)); err != nil {
			log.Println("could not send error for cannot call resume on job which doesnt exist", err)
		}
		return
	}

	// return the number of bytes that have been processed
	w.WriteHeader(http.StatusOK)
	log.Printf("sent resume bytes of %d for %s", job.BytesProcessed, job.Name)
	if _, err := w.Write([]byte(fmt.Sprintf("%d", job.BytesProcessed))); err != nil {
		log.Printf("could not send resume bytes for %s: %v", job.Name, err)
	}
}

func (s *Server) done(w http.ResponseWriter, r *http.Request) {
	// make sure it's a post
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	jobName := r.Header.Get("Job")

	s.JobsLock.RLock()
	job, ok := s.Jobs[jobName]
	s.JobsLock.RUnlock()

	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		msg := "cannot call done on job which doesn't exist"
		log.Println(msg)
		if _, err := w.Write([]byte(msg)); err != nil {
			log.Printf("%s - %v", msg, err)
		}
		return
	}
	// copy is done, close pipe
	if err := job.Stdin.Close(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Printf("sent error 5:  %v", err)
		if _, err := w.Write([]byte(err.Error())); err != nil {
			log.Printf("%s - %v", "failed sending back sent error 5", err)
		}
		return
	}

	// wait for the job to finish
	if err := job.CommandHandler.Wait(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Printf("sent error 6: %v", err)
		if _, err := w.Write([]byte(err.Error())); err != nil {
			log.Printf("%s - %v", "failed sending back sent error 6", err)
		}
		return
	}

	// delete it from the map
	s.JobsLock.Lock()
	delete(s.Jobs, jobName)
	s.JobsLock.Unlock()

	// successful done
	w.WriteHeader(http.StatusOK)
	log.Printf("finished processing job %s", jobName)
	if _, err := w.Write([]byte("ok")); err != nil {
		log.Printf("%s - %v", "failed sending back finished processing job", err)
	}
}

func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	// make sure it's a post
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// get the job
	jobName := r.Header.Get("Job")
	chunkSize, err := strconv.Atoi(r.Header.Get("Chunk-Size"))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Printf("sent error stdin: %v", err)
		if _, err := w.Write([]byte(err.Error())); err != nil {
			log.Printf("%s - %v", "failed sending back sent error stdin", err)
		}
		return
	}

	s.JobsLock.RLock()
	job, ok := s.Jobs[jobName]
	s.JobsLock.RUnlock()

	// have we seen this job before?
	// no - this is an error since it should have been created first
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		msg := fmt.Sprintf("unknown job: %s", jobName)
		log.Println(msg)
		if _, err := w.Write([]byte(msg)); err != nil {
			log.Printf("%s - %v", "failed sending back unknown job", err)
		}
		return
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
		if _, err := w.Write([]byte(err.Error())); err != nil {
			log.Printf("%s - %v", "failed sending back sent error 3", err)
		}
		return
	}

	if b <= 0 {
		w.WriteHeader(http.StatusInternalServerError)
		log.Printf("sent error 4: %d: %s", b, "no body found")
		if _, err := w.Write([]byte("no body found")); err != nil {
			log.Printf("%s - %v", "failed sending back sent error 4", err)
		}
		return
	}

	if b != int64(chunkSize) {
		w.WriteHeader(http.StatusInternalServerError)
		log.Printf("sent error 5: unexpected number of bytes. expected: %d, got: %d", chunkSize, b)
		if _, err := w.Write([]byte("unexpected number of bytes")); err != nil {
			log.Printf("%s - %v", "failed sending back unexpected number of bytes", err)
		}
		return
	}

	// pass the data to the program
	b, err = io.Copy(job.Stdin, buffer)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Printf("sent error 6: %d: %v", b, err)
		if _, err := w.Write([]byte(err.Error())); err != nil {
			log.Printf("%s - %v", "failed sending back sent error 6", err)
		}
		return
	}
	if b <= 0 || b != int64(chunkSize) {
		w.WriteHeader(http.StatusInternalServerError)
		log.Printf("sent error 7: could not send all bytes from buffer to job stdin. chunkSize: %d, b: %d", chunkSize, b)
		if _, err := w.Write([]byte("could not send all bytes from buffer to job stdin")); err != nil {
			log.Printf("%s - %v", "failed sending back sent error 7", err)
		}
		return
	}

	// successful chunk
	job.BytesProcessed += uint64(chunkSize)

	w.WriteHeader(http.StatusOK)
	log.Printf("finished processing chunk for job %s. size: %d", jobName, b)
	if _, err := w.Write([]byte("ok")); err != nil {
		log.Printf("%s - %v", "failed sending back finished processing", err)
	}
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
		mux.HandleFunc("/new", s.new)       // make a new job
		mux.HandleFunc("/upload", s.upload) // upload data to a job
		mux.HandleFunc("/resume", s.resume) // get details to resume a job
		mux.HandleFunc("/done", s.done)     // finish a job

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
