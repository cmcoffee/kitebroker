package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/textproto"
	"net/url"
	"os"
	"time"
)

// KiteBroker Flags
const (
	READY = 1 << iota
	WORKING
	COMPLETE
	
)

type FileRecord struct {
	Flag 	  int 		 `json:"flag"`
	Timestamp *time.Time `json:"timestamp"`
	Chunks 	  []int      `json:"file_chunks"`
}

type ftransfer struct {
	c_size int
	in     io.ReadCloser
	out    io.WriteCloser
}

func NewReaderWriter(in io.ReadCloser, out io.WriteCloser) *ftransfer {
	return &ftransfer{0, in, out}
}

// Perform the file transfer, input stream->output stream.
func Transfer(size *int64, reader io.Reader, writer io.Writer) (err error) {

	buf := make([]byte, 2048)
	var eof bool

	for {
		rb, err := reader.Read(buf)
		if err != nil && err == io.EOF {
			eof = true
		} else if err != nil {
			return err
		}
		n := 0
		for {
			wb, err := writer.Write(buf[n:rb])
			if size != nil {
				*size = *size + int64(wb)
			}
			if err != nil {
				return err
			}
			n = n + wb
			if n == rb {
				break
			}
		}
		if eof {
			return nil
		}
	}
	return nil
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


func (s Session) DownloadFile(folder_id int, file_id int) (err error) {
	_, err = s.FileInfo(file_id)
	if err != nil { return err }

	var record FileRecord
	_, err = DB.Get("files", file_id, &record)

	if record.Flag&COMPLETE > 0 {
		return nil
	}
	return	
}

/*
// Downloads a file to a specific path
func (j *Job) Download(s Session, file_id int, local_path string) (err error) {
	<-transfer_call_bank
	defer func() { transfer_call_bank <- call_done }()

	nfo, err := s.FileInfo(file_id)
	if err != nil {
		return
	}

	var record FileRecord
	_, err = DB.Get(j.table("files"), file_id, &record)
	if err != nil {
		return err
	}

	if record.Flag&COMPLETED > 0 {
		return nil
	}

	var f *os.File

	local_path = cleanPath(local_path)

	fname := fmt.Sprintf("%s/%s", local_path, nfo.Name)
	temp_fname := fmt.Sprintf("%s/%s.%d.incomplete", cleanPath(Config.SGet(NAME, "temp_path")), nfo.Name, file_id)

	var offset int64

	fstat, err := os.Stat(temp_fname)
	if err == nil || !os.IsNotExist(err) {
		offset = fstat.Size()
	}

	f, err = os.OpenFile(temp_fname, os.O_CREATE|os.O_RDWR, 0755)
	if err != nil {
		return
	}

	req, err := s.NewRequest("GET", fmt.Sprintf("/rest/files/%d/content", file_id))
	if err != nil {
		return
	}

	req.Header.Set("Content-Type", "application/octet-stream")
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}

	client := s.NewClient()
	resp, err := client.Do(req)
	if err != nil && resp.StatusCode != 200 {
		return
	}

	// Resume transfer if we've already started downloading a file
	if offset > 0 {
		start := resp.Header.Get("Content-Range")
		if start == NONE {
			if record.Flag&DOWNLOADED > 0 {
				goto MoveFile
			}
			return
		}
		start = strings.TrimPrefix(start, "bytes")
		byte_range := strings.Split(start, "-")
		start = byte_range[0]
		start = strings.TrimSpace(start)
		offset, err = strconv.ParseInt(start, 10, 64)
		if err != nil {
			return
		}
		_, err = f.Seek(offset, 0)
		if err != nil {
			return
		}
	}

	defer resp.Body.Close()

	fmt.Printf("\r(%s) Downloading file %s[%s] to %s.\n", j.name, nfo.Name, showSize(nfo.Size), local_path)

	err = Transfer(nil, resp.Body, f)
	if err != nil {
		return
	}

	err = s.DecodeJSON(resp, nil)
	if err != nil {
		return
	}

	record.Flag |= COMPLETED

	DB.Set(j.table("files"), file_id, record)

MoveFile:

	// Close the file stream.
	if err = f.Close(); err != nil {
		return
	}

	// Rename file.
	if err = moveFile(temp_fname, fname); err != nil {
		return
	}

	record.Flag |= MOVED

	DB.Set(j.table("files"), file_id, record)

	mtime, err := timeParse(nfo.Modified)
	if err != nil {
		return
	}

	// Set modified and access times on file.
	err = os.Chtimes(fname, time.Now(), mtime)
	return
}
*/
// Multipart filestreamer
type streamReadCloser struct {
	r_buff []byte
	w_buff *bytes.Buffer
	reader io.Reader
	writer io.Writer
	eof    bool
	*multipart.Writer
}

func (s *streamReadCloser) Read(p []byte) (n int, err error) {
	buf_len := s.w_buff.Len()

	if buf_len > 0 {
		return s.w_buff.Read(p)
	}

	// Clear our output buffer.
	s.w_buff.Truncate(0)

	if s.eof {
		s.Close()
		return 0, io.EOF
	}

	n, err = s.reader.Read(s.r_buff)
	if err != nil && err == io.EOF {
		s.eof = true
	} else if err != nil {
		return -1, err
	}

	if n > 0 {
		n, err = s.w_buff.Write(s.r_buff)
		if err != nil {
			return -1, err
		}
		for i := 0; i < len(s.r_buff); i++ {
			s.r_buff[i] = 0
		}
	}

	return s.w_buff.Read(p)
}

// Create a mime multipart upload
func NewMultipart(file *os.File, input interface{}) (io.ReadCloser, string, error) {
	r_buff := make([]byte, 1024)
	w_buff := new(bytes.Buffer)
	w := multipart.NewWriter(w_buff)
	mimeheader := make(textproto.MIMEHeader)
	switch i := input.(type) {
	case PostFORM:
		p := make(url.Values)
		for k, v := range i {
			p.Add(k, v)
		}
		mimeheader.Set("Content-Disposition", "form-data; name=\"text\"")
		writer, err := w.CreatePart(mimeheader)
		if err != nil {
			return nil, NONE, err
		}
		writer.Write([]byte(p.Encode()))
	case PostJSON:
		json, err := json.Marshal(i)
		if err != nil {
			return nil, NONE, err
		}
		if call_snoop {
			fmt.Println(string(json))
		}
		mimeheader.Set("Content-Type", "application/json")
		mimeheader.Set("Content-Disposition", "form-data; name=\"attributes\"")
		writer, err := w.CreatePart(mimeheader)
		if err != nil {
			return nil, NONE, err
		}
		writer.Write(json)
	}
	writer, err := w.CreateFormFile("file", file.Name())
	if err != nil {
		return nil, NONE, err
	}
	return &streamReadCloser{
		r_buff,
		w_buff,
		file,
		writer,
		false,
		w,
	}, w.Boundary(), nil
}

// Uploads file from specific Path
func (s Session) Upload(folder_id int, local_file string) (err error) {
	<-transfer_call_bank
	defer func() { transfer_call_bank <- call_done }()

	//	fmt.Println(uri)

	req, err := s.NewRequest("POST", fmt.Sprintf("/rest/folders/%d/actions/file", folder_id))
	if err != nil {
		return
	}

	f, err := os.Open(local_file)
	if err != nil {
		return
	}

	defer f.Close()

	var boundary string

	req.Body, boundary, err = NewMultipart(f, nil)
	if err != nil {
		return
	}

	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)

	client := s.NewClient()
	resp, err := client.Do(req)
	if err != nil && resp.StatusCode != 200 {
		return
	}
	err = s.DecodeJSON(resp, nil)
	return
}
