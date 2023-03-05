package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
)

func newfileUploadRequest(args Args) (*http.Request, error) {
	r, w := io.Pipe()

	request, err := http.NewRequest("POST", args.Url, r)
	if err != nil {
		return nil, err
	}
	go func() {
		_, err := io.Copy(w, os.Stdin)
		w.CloseWithError(err)
	}()

	request.Header.Set("Job", args.Job)
	request.Header.Set("Command", args.Command)
	request.Header.Set("Content-Type", "application/octet-stream")
	return request, nil
}

func validate(args Args) error {
	// valid url
	if _, err := url.ParseRequestURI(args.Url); err != nil {
		return err
	}
	// valid job name
	if strings.TrimSpace(args.Job) == "" {
		return fmt.Errorf("invalid job name")
	}
	// valid command
	if strings.TrimSpace(args.Command) == "" {
		return fmt.Errorf("invalid command")
	}
	return nil
}

type Args struct {
	Url     string
	Job     string
	Command string
}

func main() {
	var args Args
	flag.StringVar(&args.Url, "url", "", "url of rpiped")
	flag.StringVar(&args.Job, "job", "", "name of job (to resume in the future)")
	flag.StringVar(&args.Command, "command", "", "command to run on rpiped")
	flag.Parse()
	if err := validate(args); err != nil {
		log.Fatalln(err)
	}
	r, err := newfileUploadRequest(args)
	if err != nil {
		log.Fatalln(err)
	}
	client := &http.Client{}

	resp, err := client.Do(r)

	if err != nil {
		log.Fatalln(err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalln(err)
	}
	log.Println(string(body))
}
