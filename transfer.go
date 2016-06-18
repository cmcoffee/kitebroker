package main

import (
	"fmt"
	"io"
	"os"
)

type ftransfer struct {
	c_size int
	in     io.ReadCloser
	out    io.WriteCloser
}

func NewReaderWriter(in io.ReadCloser, out io.WriteCloser) *ftransfer {
	return &ftransfer{0, in, out}
}

func getPercentage(t_size, c_size int) int {
	return int((float64(c_size) / float64(t_size)) * 100)
}

// Perform the file transfer, input stream->output stream.
func (f *ftransfer) Transfer() error {

	var eof_count, n int
	buff := make([]byte, 1024)

	for eof_count < 2 {
		//Wipe the buffer.
		for n, _ := range buff {
			buff[n] = 0
		}

		in_sz, err := f.in.Read(buff)
		if err != nil {
			if err == io.EOF {
				eof_count++
			}
			if err != io.EOF {
				return err
			}
		}

		// Write it to disk.
		n = 0
		for n < in_sz {
			out_sz, err := f.out.Write(buff[n:in_sz])
			f.c_size = f.c_size + out_sz
			n = out_sz
			if err != nil {
				return err
			}
		}

	}
	// Close the input stream.
	return f.in.Close()
}

// Will use later.
func FileExists(f string) (found bool, err error) {
	if _, err = os.Stat(f); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return true, err
}

// Downloads a file to a specific path
func (s Session) Download(file_id int, local_path string) (err error) {
	nfo, err := s.FileInfo(file_id)
	if err != nil {
		return err
	}

	f, err := os.Create(fmt.Sprintf("%s/%s", local_path, nfo.Name))
	if err != nil {
		return err
	}

	req, err := s.NewRequest("GET", fmt.Sprintf("/rest/files/%d/content", file_id))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/octet-stream")

	client := s.NewClient()
	resp, err := client.Do(req)
	if err != nil && resp.StatusCode != 200 {
		return err
	}

	t := NewReaderWriter(resp.Body, f)
	err = t.Transfer()
	if err != nil {
		return err
	}

	// Close the file stream.
	err = f.Close()

	return
}
