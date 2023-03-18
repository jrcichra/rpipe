package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"time"

	"github.com/oklog/run"
)

type Args struct {
	Daemon      bool
	Addr        string
	MetricsAddr string
}

type Job struct {
	Command        string
	CommandHandler *exec.Cmd
	Stdin          io.WriteCloser
}

type Server struct {
	Jobs map[string]*Job
}

func NewServer() *Server {
	return &Server{
		Jobs: make(map[string]*Job),
	}
}

func (s *Server) do(w http.ResponseWriter, r *http.Request) {
	// make sure it's a post
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// get the job and command
	jobName := r.Header.Get("Job")
	command := r.Header.Get("Command")
	resume := r.Header.Get("Resume")

	job, ok := s.Jobs[jobName]
	// have we seen this job before?
	if !ok {
		splitCommand := strings.Split(command, " ")
		// start a new job
		job = &Job{
			CommandHandler: exec.Command(splitCommand[0], splitCommand[1:]...),
			Command:        command,
		}
		// associate it
		s.Jobs[jobName] = job

		// grab stdin
		var err error
		job.Stdin, err = job.CommandHandler.StdinPipe()
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			log.Printf("sent error 1: %v", err)
			w.Write([]byte(err.Error()))
			return
		}
	}

	log.Printf("processing job %s...", jobName)

	// start the job if we haven't already
	if resume != "yes" {
		if err := job.CommandHandler.Start(); nil != err {
			w.WriteHeader(http.StatusInternalServerError)
			log.Printf("sent error 2: %v", err)
			w.Write([]byte(err.Error()))
			return
		}
	}

	// copy data to command program
	if b, err := io.Copy(job.Stdin, r.Body); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Printf("sent error 3: %d: %v", b, err)
		w.Write([]byte(err.Error()))
		return
	}

	// copy is done, close pipe
	if err := job.Stdin.Close(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Printf("sent error 5:  %v", err)
		w.Write([]byte(err.Error()))
		return
	}

	// delete it from the map
	delete(s.Jobs, jobName)
	// wait for the job to finish
	if err := job.CommandHandler.Wait(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Printf("sent error 5: %v", err)
		w.Write([]byte(err.Error()))
		return
	}

	// success
	w.WriteHeader(http.StatusOK)
	log.Printf("finished processing job %s", jobName)
	w.Write([]byte("ok"))
}

func main() {
	var args Args
	flag.BoolVar(&args.Daemon, "daemon", false, "run as daemon executing commands")
	flag.StringVar(&args.Addr, "bind", ":8000", "bind addr for rpipe jobs")
	flag.StringVar(&args.MetricsAddr, "metrics", ":2100", "bind addr for metrics")
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
		mux.HandleFunc("/", s.do)

		ln, err := net.Listen("tcp", args.Addr)
		if err != nil {
			log.Fatalln(err)
		}
		g.Add(func() error {
			log.Printf("listening on %s...\n", args.Addr)
			server := http.Server{
				ReadTimeout:       5 * time.Second,
				WriteTimeout:      5 * time.Second,
				IdleTimeout:       5 * time.Second,
				ReadHeaderTimeout: 5 * time.Second,
				Handler:           mux,
			}
			return server.Serve(ln)
		}, func(err error) {
			ln.Close()
		})
	}

	// run the daemons
	if err := g.Run(); err != nil {
		log.Println(err)
	}

}
