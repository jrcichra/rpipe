package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// keeps track of the previous read
type CountingReader struct {
	r        io.Reader
	previous []byte
}

func (cr *CountingReader) Read(p []byte) (int, error) {
	n, err := cr.r.Read(p)
	// log.Println(len(p))
	cr.previous = make([]byte, len(p))
	copy(cr.previous, p)
	return n, err
}

func (cr *CountingReader) ReadPrevious() io.Reader {
	return bytes.NewReader(cr.previous)
}

type Args struct {
	Url               string
	Job               string
	Command           string
	AdditionalHeaders string
}

type Client struct {
	httpClient     http.Client
	args           Args
	countingReader *CountingReader
}

func NewClient(args Args) *Client {
	return &Client{
		httpClient: http.Client{Timeout: 5 * time.Second},
		args:       args,
	}
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

func (c *Client) handleHTTPConnection(resume bool) (string, error) {
	request, err := http.NewRequest("POST", c.args.Url, c.countingReader)
	if err != nil {
		return "", err
	}
	client := &http.Client{}
	request.Header.Set("Job", c.args.Job)
	request.Header.Set("Command", c.args.Command)
	if resume {
		request.Header.Set("Resume", "yes")
	}
	request.Header.Set("Content-Type", "application/octet-stream")

	resp, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (c *Client) uploadStream() error {
	resume := false
	for {
		if resume {
			c.countingReader = &CountingReader{r: io.MultiReader(c.countingReader.ReadPrevious(), os.Stdin)}
		} else {
			c.countingReader = &CountingReader{r: io.MultiReader(os.Stdin)}
		}

		_, err := c.handleHTTPConnection(resume)
		if err != nil {
			// something went wrong with the http connection
			// spin up a new one marked as resume
			log.Println(err)
			resume = true
		} else {
			// no error - data transfer must have completed successfully
			return nil
		}
		// give some time between requests
		time.Sleep(1 * time.Second)
	}
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

	client := NewClient(args)
	if err := client.uploadStream(); err != nil {
		log.Fatalln(err)
	}
	log.Println("file transfer complete")
}
