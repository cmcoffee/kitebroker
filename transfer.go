package main

import (
	"bytes"
	"fmt"
	"github.com/cmcoffee/go-logger"
	"io"
	"mime/multipart"
	"os"
	"strconv"
	"strings"
	"time"
)

// KiteBroker Flags
const (
	UPLOAD = 1 << iota
	DONE
)

type UploadRecord struct {
	Flag            int       `json:"flag"`
	CompletedChunks int       `json:"chunks_completed"`
	TotalChunks     int       `json:"total_chunks"`
	ChunkSize       int64     `json:"chunk_size"`
	LastChunk       int64     `json:"last_chunk"`
	TotalSize       int64     `json:"total_size"`
	ModTime         time.Time `json:"modified"`
	URI             string    `json:"upload_uri"`
	Filename        string    `json:"filename"`
}

type DownloadRecord struct {
	Flag    int64     `json:"flag"`
	ModTime time.Time `json:"modified"`
}

// Returns total number of chunks and last chunk size.
func (t Task) getChunkInfo(sz int64) (total_chunks int, last_chunk int64) {

	chunk_size_megs, err := strconv.Atoi(Config.SGet(t.task_id, "chunk_megabytes"))
	if err != nil {
		chunk_size_megs = 64
	}

	chunk_size := int64(chunk_size_megs) * 1024 * 1024

	if sz < 0 {
		return 0, chunk_size
	}

	if sz <= chunk_size {
		return 1, sz
	}
	for {
		total_chunks++
		if sz > chunk_size {
			sz = sz - chunk_size
			continue
		} else {
			last_chunk = sz
			return
		}
	}
}

// Provides human readable file sizes.
func showSize(bytes int64) string {

	names := []string{
		"Bytes",
		"KB",
		"MB",
		"GB",
	}

	suffix := 0
	size := float64(bytes)

	for size >= 1000 && suffix < len(names)-1 {
		size = size / 1000
		suffix++
	}

	return fmt.Sprintf("%.1f%s", size, names[suffix])
}

func showRate(size int64, start_time time.Time) string {

	names := []string{
		"bps",
		"kbps",
		"mbps",
		"gbps",
	}

	suffix := 0

	sz := float64(size) / time.Since(start_time).Seconds()

	for sz >= (1000) && suffix < len(names)-1 {
		sz = sz / 1000
		suffix++
	}

	if sz != 0.0 {
		return fmt.Sprintf("%.1f%s", sz*8, names[suffix])
	} else {
		return "Complete"
	}
}

func progressBar(c_size, t_size int64) string {
	num := int((float64(c_size) / float64(t_size)) * 100)
	if t_size == 0 {
		num = 100
	}
	var display [25]rune
	for n := range display {
		if n < num/4 {
			display[n] = '░'
		} else {
			display[n] = '.'
		}
	}
	return fmt.Sprintf("[%s] %d%%", string(display[0:]), int(num))
}

func NewTMonitor(title string, total_sz int64) *TMonitor {
	return &TMonitor{
		name:       title,
		total_size: total_sz,
		transfered: 0,
		offset:     0,
		start_time: time.Now(),
		last_shown: time.Now(),
	}
}

type TMonitor struct {
	name       string
	total_size int64
	transfered int64
	offset     int64
	start_time time.Time
	last_shown time.Time
}

func (t *TMonitor) RecordTransfer(current_sz int) {
	t.transfered = t.transfered + int64(current_sz)
	if time.Since(t.last_shown) < time.Second {
		return
	}
	t.ShowTransfer()
}

func (t *TMonitor) ShowTransfer() {
	if t.total_size > -1 {
		logger.Put(fmt.Sprintf("(%s) %s %s (%s/%s)", t.name, showRate(t.transfered-t.offset, t.start_time), progressBar(t.transfered, t.total_size), showSize(t.transfered), showSize(t.total_size)))
	} else {
		logger.Put(fmt.Sprintf("(%s) %s (%s)", t.name, showRate(t.transfered-t.offset, t.start_time), showSize(t.transfered)))
	}
	t.last_shown = time.Now()
}

func (t *TMonitor) Offset(current_sz int64) {
	t.transfered = current_sz
	t.offset = t.transfered
}

// Perform the file transfer, input stream->output stream.
func Transfer(reader io.Reader, writer io.Writer, tm *TMonitor) (err error) {

	buf := make([]byte, 4096)
	var eof bool

	for {
		rb, err := reader.Read(buf)
		if err != nil && err == io.EOF {
			eof = true
		} else if err != nil {
			return err
		}
		if rb > -1 {
			tm.RecordTransfer(rb)
		}
		n := 0
		for {
			wb, err := writer.Write(buf[n:rb])
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

// Downloads a file to a specific path
func (s *Session) Download(nfo KiteData) (err error) {
	var record DownloadRecord
	_, err = DB.Get("dl_files", nfo.ID, &record)
	if err != nil {
		return err
	}

	mtime, err := read_kw_time(nfo.Modified)
	if err != nil {
		return err
	}

	if record.ModTime.IsZero() || time.Since(mtime) > time.Since(record.ModTime) {
		DB.Unset("dl_files", nfo.ID)
		record.Flag = 0
		record.ModTime = mtime
		DB.Set("dl_files", nfo.ID, &record)
	}

	if record.Flag&DONE == DONE {
		return ErrDownloaded
	}

	var f *os.File

	var local_path string

	found, err := DB.Get("dl_folders", nfo.ParentID, &local_path)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("Cannot download file %s, local destination folder missing.", nfo.Name)
	}

	local_path = cleanPath(local_path)

	fname := cleanPath(fmt.Sprintf("%s/%s", local_path, nfo.Name))
	temp_fname := cleanPath(fmt.Sprintf("%s/%s.%d.incomplete", cleanPath(Config.SGet("configuration", "temp_path")), nfo.Name, nfo.ID))

	var offset int64

	fstat, err := os.Stat(temp_fname)
	if err == nil || !os.IsNotExist(err) {
		offset = fstat.Size()
	}

	f, err = os.OpenFile(temp_fname, os.O_CREATE|os.O_RDWR, 0755)
	if err != nil {
		return
	}

	req, err := s.NewRequest("GET", fmt.Sprintf("/rest/files/%d/content", nfo.ID))
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
		return err
	}
	defer resp.Body.Close()

	if resp.ContentLength == -1 {
		err = s.respError(resp)
		if err != nil {
			return err
		}
		logger.Warn("Content-Length header was less than zero.")
	}

	tm := NewTMonitor("download", nfo.Size)
	tm.Offset(offset)

	HideLoader()
	logger.Log("Downloading %s(%s).\n", nfo.Name, showSize(nfo.Size))
	tm.ShowTransfer()

	// Resume transfer if we've already started downloading a file
	if offset > 0 {
		start := resp.Header.Get("Content-Range")
		if start == NONE {
			goto MoveFile
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

	err = Transfer(resp.Body, f, tm)
	if err != nil {
		return
	}

	err = s.DecodeJSON(resp, nil)
	if err != nil {
		return
	}

	record.ModTime, err = read_kw_time(nfo.Modified)
	if err != nil {
		return
	}

	DB.Set("dl_files", nfo.ID, record)

MoveFile:
	tm.ShowTransfer()
	fmt.Println(NONE)
	logger.Log("Download completed succesfully.")
	ShowLoader()

	// Close the file stream.
	f.Close()

	// Rename file.
	if err = moveFile(temp_fname, fname); err != nil {
		return
	}

	record.Flag |= DONE

	DB.Set("dl_files", nfo.ID, record)

	// Set modified and access times on file.
	err = os.Chtimes(fname, time.Now(), record.ModTime)
	return
}

// Multipart filestreamer
type streamReadCloser struct {
	limit    int64
	size     int64
	r_buff   []byte
	w_buff   *bytes.Buffer
	reader   io.Reader
	eof      bool
	f_writer io.Writer
	tm       *TMonitor
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
		return 0, io.EOF
	}

	n, err = s.reader.Read(s.r_buff)
	if err != nil && err == io.EOF {
		s.eof = true
		s.Close()
	} else if err != nil {
		return -1, err
	}
	s.size = s.size + int64(n)

	if n > 0 {
		if s.size > s.limit {
			n = int(int64(n) - (s.size - s.limit))
			s.eof = true
		}
		s.tm.RecordTransfer(n)
		n, err = s.f_writer.Write(s.r_buff[0:n])
		if err != nil {
			return -1, err
		}
		if s.eof {
			s.Close()
		}
		for i := 0; i < len(s.r_buff); i++ {
			s.r_buff[i] = 0
		}
	}
	return s.w_buff.Read(p)
}

// Uploads file from specific local path, uploads in chunks, allows resume.
func (t *Task) Upload(local_file string, folder_id int) (err error) {
	local_path := cleanPath(local_file)

	fstat, err := os.Stat(local_path)
	if err != nil {
		return err
	}

	r_path := strings.TrimLeft(local_file, Config.SGet(t.task_id, "local_path"))

	var record UploadRecord
	_, err = DB.Get("uploads", r_path, &record)
	if err != nil {
		return err
	}

	_, chunk_size := t.getChunkInfo(-1)

	// Create a record in the database if one does not exist yet or does not appear to be the one we've uploaded.
	if record.Flag == 0 || record.ModTime.UTC() != fstat.ModTime().UTC() || record.TotalSize != fstat.Size() || record.ChunkSize != chunk_size && record.Flag&DONE != DONE {
		record.Flag = UPLOAD
		record.CompletedChunks = 0
		record.TotalSize = fstat.Size()
		record.ModTime = fstat.ModTime()
		record.TotalChunks, record.LastChunk = t.getChunkInfo(fstat.Size())
		record.ChunkSize = chunk_size
		record.Filename = fstat.Name()
		record.URI, err = t.session.NewFile(folder_id, fstat.Name(), record.TotalSize, record.TotalChunks, record.ModTime)
		if err != nil {
			return err
		}
		DB.Set("uploads", r_path, &record)
	}

	if record.Flag == DONE && fstat.Size() == record.TotalSize {
		return ErrUploaded
	}

	f, err := os.Open(local_file)
	if err != nil {
		return
	}
	defer f.Close()

	var limit int64
	var offset int64

	w_buff := new(bytes.Buffer)

	tm := NewTMonitor("upload", record.TotalSize)
	tm.Offset(record.ChunkSize * int64(record.CompletedChunks))

	var resp_data *KiteData

	HideLoader()

	logger.Log("Uploading %s(%s).", local_file, showSize(record.TotalSize))

	for record.CompletedChunks < record.TotalChunks {
		w_buff.Reset()

		offset = record.ChunkSize * int64(record.CompletedChunks)
		_, err = f.Seek(offset, 0)
		if err != nil {
			return err
		}

		req, err := t.session.NewRequest("POST", fmt.Sprintf("/%s?apiVersion=5", record.URI))
		if err != nil {
			return err
		}

		w := multipart.NewWriter(w_buff)

		req.Header.Set("Content-Type", "multipart/form-data; boundary="+w.Boundary())

		if record.CompletedChunks < (record.TotalChunks - 1) {
			limit = record.ChunkSize
		} else {
			limit = record.LastChunk
		}

		err = w.WriteField("compressionMode", "NORMAL")
		if err != nil {
			return err
		}

		err = w.WriteField("index", fmt.Sprintf("%d", record.CompletedChunks+1))
		if err != nil {
			return err
		}

		err = w.WriteField("compressionSize", fmt.Sprintf("%d", limit))
		if err != nil {
			return err
		}

		err = w.WriteField("originalSize", fmt.Sprintf("%d", limit))
		if err != nil {
			return err
		}

		f_writer, err := w.CreateFormFile("content", record.Filename)
		if err != nil {
			return err
		}

		post := &streamReadCloser{
			limit,
			0,
			make([]byte, 4096),
			w_buff,
			f,
			false,
			f_writer,
			tm,
			w,
		}

		req.Body = post

		client := t.session.NewClient()
		resp, err := client.Do(req)
		if err != nil {
			return err
		}

		err = t.session.DecodeJSON(resp, &resp_data)
		if err != nil {
			return err
		}

		record.CompletedChunks++
		err = DB.Set("uploads", r_path, &record)
		if err != nil {
			return err
		}
	}
	record.Flag = DONE
	err = DB.Set("uploads", r_path, &record)
	if err != nil {
		return err
	}
	tm.ShowTransfer()
	fmt.Println(NONE)
	logger.Log("Upload complete.")
	if strings.ToLower(Config.SGet(t.task_id, "delete_source_files_on_complete")) == "yes" {
		logger.Log("Remvoing local file %s.", local_file)
		err = os.Remove(local_file)
	}
	ShowLoader()
	return
}
