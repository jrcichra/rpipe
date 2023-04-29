package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/google/uuid"
)

type Args struct {
	Url               string
	Command           string
	AdditionalHeaders string
	ChunkSize         int
	ResumeJobID       string
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

func (c *Client) sendDone(jobID string) error {
	request, err := http.NewRequest("POST", c.args.Url+"/done", nil)
	if err != nil {
		return err
	}
	var client http.Client
	request.Header.Set("Job", jobID)

	// add additional headers if here are any
	for key, value := range c.additionalHeaders {
		request.Header.Set(key, value)
	}

	resp, err := client.Do(request)
	if err != nil {
		return err
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if string(body) != "ok" {
		return errors.New("did not receive ok from server")
	}
	return nil
}

func (c *Client) handleHTTPSession(jobID string, reader *bufio.Reader) error {
	for {
		buffer, err := reader.Peek(c.args.ChunkSize)
		if err != nil && len(buffer) == 0 {
			return err
		}
		chunkReader := bytes.NewReader(buffer)
		request, err := http.NewRequest("POST", c.args.Url+"/upload", chunkReader)
		if err != nil {
			return err
		}
		chunkLen := chunkReader.Len()
		var client http.Client
		request.Header.Set("Job", jobID)
		request.Header.Set("Chunk-Size", strconv.Itoa(chunkLen))
		request.Header.Set("Content-Type", "application/octet-stream")

		// add additional headers if here are any
		for key, value := range c.additionalHeaders {
			request.Header.Set(key, value)
		}

		log.Println("uploading chunk...")
		resp, err := client.Do(request)
		if err != nil {
			return err
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		if string(body) != "ok" {
			return errors.New("did not receive ok from server")
		}
		// discard what was peeked
		if _, err := reader.Discard(chunkLen); err != nil {
			return err
		}
		log.Println("successfully uploaded chunk")
	}
}

func (c *Client) newJob() (string, error) {
	request, err := http.NewRequest("POST", c.args.Url+"/new", nil)
	if err != nil {
		return "", err
	}
	var client http.Client

	// send the command we're going to run
	request.Header.Set("Command", c.args.Command)
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
	// make sure body is valid uuid
	sBody := string(body)
	if _, err := uuid.Parse(sBody); err != nil {
		return "", err
	}
	return sBody, nil
}

func (c *Client) resumeJob(jobID string) (uint64, error) {
	request, err := http.NewRequest("GET", c.args.Url+"/resume", nil)
	if err != nil {
		return 0, err
	}
	var client http.Client

	// set the job id we're going to resume
	request.Header.Set("Job", jobID)
	// add additional headers if here are any
	for key, value := range c.additionalHeaders {
		request.Header.Set(key, value)
	}

	resp, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	// make sure body is valid uint64
	return strconv.ParseUint(string(body), 10, 64)
}

func (c *Client) uploadStream() error {
	// start a new job unless we're resuming
	var jobID string
	var skip uint64
	if c.args.ResumeJobID == "" {
		var err error
		jobID, err = c.newJob()
		if err != nil {
			return err
		}
	} else {
		jobID = c.args.ResumeJobID
		var err error
		skip, err = c.resumeJob(jobID)
		if err != nil {
			return err
		}
	}

	stdinReader := bufio.NewReaderSize(os.Stdin, c.args.ChunkSize)

	// skip bytes if resuming
	{
		skipLeft := skip
		for {
			if skipLeft <= 0 {
				break
			}
			skipped, err := stdinReader.Discard(int(skipLeft))
			skipLeft -= uint64(skipped)
			if err != nil {
				return err
			}
		}
	}

	// send all the data
	{
		b := backoff.NewExponentialBackOff()
		b.MaxElapsedTime = 0
		b.MaxInterval = time.Minute * 1
		if err := backoff.Retry(func() error {
			if err := c.handleHTTPSession(jobID, stdinReader); err != nil {
				if err == io.EOF {
					// we've hit the end
					return nil
				}
				// something else went wrong with the session
				log.Println(err)
				return err
			}
			// hit the end in some other way
			log.Println("shouldn't be here")
			return nil
		}, b); err != nil {
			return err
		}
	}

	// tell the server we're done
	{
		b := backoff.NewExponentialBackOff()
		b.MaxElapsedTime = 0
		b.MaxInterval = time.Minute * 1
		if err := backoff.Retry(func() error {
			if err := c.sendDone(jobID); err != nil {
				log.Println(err)
			}
			// no error - server must know we're done
			return nil
		}, b); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	var args Args
	flag.StringVar(&args.Url, "url", "", "url of rpiped")
	flag.StringVar(&args.Command, "command", "", "command to run on rpiped")
	flag.StringVar(&args.AdditionalHeaders, "headers", "", "additional headers")
	flag.IntVar(&args.ChunkSize, "chunk-size", 10, "chunk size (in MB) for requests")
	flag.StringVar(&args.ResumeJobID, "resume-job-id", "", "resume job id instead of starting a new job")
	flag.Parse()
	args.ChunkSize *= 1024 * 1024
	if err := validate(args); err != nil {
		log.Fatalln(err)
	}

	client := NewClient(args)
	if err := client.uploadStream(); err != nil {
		log.Fatalln(err)
	}
	log.Println("file transfer complete")
}
