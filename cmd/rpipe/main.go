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
		var client http.Client
		request.Header.Set("Job", jobID)
		request.Header.Set("Command", c.args.Command)
		request.Header.Set("Content-Type", "application/octet-stream")

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
		// discard what was peeked
		if _, err := reader.Discard(c.args.ChunkSize); err != nil {
			return err
		}
	}
}

func (c *Client) uploadStream() error {
	jobID := uuid.New().String()
	stdinReader := bufio.NewReaderSize(os.Stdin, c.args.ChunkSize)
	// send all the data
	{
		b := backoff.NewExponentialBackOff()
		b.MaxElapsedTime = 0
		backoff.Retry(func() error {
			if err := c.handleHTTPSession(jobID, stdinReader); err != nil {
				if err == io.EOF {
					// we've hit the end
					return nil
				}
				return err
			}
			// hit the end in some other way
			log.Println("shouldn't be here")
			return nil
		}, b)
	}

	// tell the server we're done
	{
		b := backoff.NewExponentialBackOff()
		b.MaxElapsedTime = 0
		backoff.Retry(func() error {
			if err := c.sendDone(jobID); err != nil {
				log.Println(err)
			}
			// no error - server must know we're done
			return nil
		}, b)
	}
	return nil
}

func main() {
	var args Args
	flag.StringVar(&args.Url, "url", "", "url of rpiped")
	flag.StringVar(&args.Command, "command", "", "command to run on rpiped")
	flag.StringVar(&args.AdditionalHeaders, "headers", "", "additional headers")
	flag.IntVar(&args.ChunkSize, "chunk-size", 10, "chunk size (in MB) for requests")
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
