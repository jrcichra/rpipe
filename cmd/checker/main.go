package main

/*

Not used yet - just for exploring some data consistency problems

*/

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"os"
)

type Args struct {
	Source    string
	Dest      string
	ChunkSize int
	Offset    int64
}

func validate(args Args) error {
	// filenames aren't empty
	if args.Source == "" || args.Dest == "" {
		return fmt.Errorf("both filenames must be specified")
	}
	return nil
}

func checkFiles(args Args) error {
	// read each file in chunksize at a time
	source, err := os.Open(args.Source)
	if err != nil {
		return err
	}
	defer source.Close()
	dest, err := os.Open(args.Dest)
	if err != nil {
		return err
	}
	defer dest.Close()

	var sourcePos int64
	var destPos int64

	// seeks
	if sourcePos, err = source.Seek(args.Offset, 0); err != nil {
		return err
	}
	if destPos, err = dest.Seek(args.Offset, 0); err != nil {
		return err
	}

	// read buffers
	sourceBuf := make([]byte, 100)
	destBuf := make([]byte, 100)

	skipSourceRead := false
	skipDestRead := false
	for {
		// fill the buffers for each
		if !skipSourceRead {
			bytes, err := source.Read(sourceBuf)
			if err != nil {
				return err
			}
			sourcePos += int64(bytes)
		}
		if !skipDestRead {
			bytes, err := dest.Read(destBuf)
			if err != nil {
				return err
			}
			destPos += int64(bytes)
		}

		// go through the data in the source by chunk

		if !bytes.Equal(sourceBuf, destBuf) {
			log.Printf("byte mismatch at position: %d == %d\n", sourcePos, destPos)
			// if we found a mismatch, skip moving the source until things start lining up working again
			// skipSourceRead = true
		} else {
			skipSourceRead = false
		}
	}
}

func main() {
	var args Args
	flag.StringVar(&args.Source, "source", "", "source file name")
	flag.StringVar(&args.Dest, "dest", "", "destination file name")
	flag.IntVar(&args.ChunkSize, "chunk-size", 10, "chunk size (in MB) for requests")
	flag.Int64Var(&args.Offset, "offset", 0, "number of bytes to offset first")
	flag.Parse()
	args.ChunkSize *= 1024 * 1024

	if err := validate(args); err != nil {
		log.Fatalln(err)
	}

	if err := checkFiles(args); err != nil {
		log.Fatalln(err)
	}

	log.Println("files are equal")
}
