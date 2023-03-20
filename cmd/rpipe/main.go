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
	"sync"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/google/uuid"
)

// keeps track of the previous read
type CountingReader struct {
	r            io.Reader
	previous     []byte
	previousLock sync.RWMutex
	tempPrevious []byte
}

func (cr *CountingReader) Read(p []byte) (int, error) {
	cr.previousLock.Lock()
	defer cr.previousLock.Unlock()
	if len(cr.previous) != len(p) {
		cr.previous = make([]byte, len(p))
	}
	n, err := cr.r.Read(p)
	copy(cr.previous, p)
	return n, err
}

func (cr *CountingReader) ReadPrevious() io.Reader {
	cr.previousLock.RLock()
	defer cr.previousLock.RUnlock()
	if len(cr.previous) != len(cr.tempPrevious) {
		cr.tempPrevious = make([]byte, len(cr.previous))
	}
	copy(cr.tempPrevious, cr.previous)
	return bytes.NewReader(cr.tempPrevious)
}

type Args struct {
	Url               string
	Command           string
	AdditionalHeaders string
}

type Client struct {
	httpClient        http.Client
	args              Args
	additionalHeaders map[string]string
}

func NewClient(args Args) *Client {
	// build headers
	additionalHeaders := make(map[string]string)
	if args.AdditionalHeaders != "" {
		headersAndValues := strings.Split(args.AdditionalHeaders, ",")
		for _, headerAndValue := range headersAndValues {
			headerAndValueSplit := strings.Split(headerAndValue, "=")
			additionalHeaders[headerAndValueSplit[0]] = headerAndValueSplit[1]
		}
	}

	return &Client{
		httpClient:        http.Client{Timeout: 5 * time.Second},
		args:              args,
		additionalHeaders: additionalHeaders,
	}
}

func validate(args Args) error {
	// valid url
	if _, err := url.ParseRequestURI(args.Url); err != nil {
		return err
	}
	// valid command
	if strings.TrimSpace(args.Command) == "" {
		return fmt.Errorf("invalid command")
	}
	return nil
}

func (c *Client) handleHTTPConnection(resume bool, reader io.Reader) (string, error) {
	request, err := http.NewRequest("POST", c.args.Url, reader)
	if err != nil {
		return "", err
	}
	client := &http.Client{}
	request.Header.Set("Job", uuid.New().String())
	request.Header.Set("Command", c.args.Command)
	if resume {
		request.Header.Set("Resume", "yes")
	}
	request.Header.Set("Content-Type", "application/octet-stream")

	// add additional headers if here are any
	for key, value := range c.additionalHeaders {
		request.Header.Set(key, value)
	}

	resp, err := client.Do(request)
	if err != nil {
		return "", err
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(body), err
}

func (c *Client) uploadStream() error {
	resume := false
	countingReader := &CountingReader{}
	b := backoff.NewExponentialBackOff()
	b.MaxElapsedTime = 0

	return backoff.Retry(func() error {
		if resume {
			log.Println("resuming connection...")
			countingReader.r = io.MultiReader(countingReader.ReadPrevious(), os.Stdin)
		} else {
			log.Println("starting connection...")
			countingReader.r = os.Stdin
		}

		responseBody, err := c.handleHTTPConnection(resume, countingReader)
		if err != nil || responseBody != "ok" {
			// something went wrong with the http connection
			resume = true
			if err == nil {
				err = fmt.Errorf(responseBody)
			}
			log.Println(err)
			return err
		}
		// no error - data transfer must have completed successfully
		return nil
	}, b)
}

func main() {
	var args Args
	flag.StringVar(&args.Url, "url", "", "url of rpiped")
	flag.StringVar(&args.Command, "command", "", "command to run on rpiped")
	flag.StringVar(&args.AdditionalHeaders, "headers", "", "additional headers")
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
