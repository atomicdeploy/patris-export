package edge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/atomicdeploy/patris-export/pkg/filecopy"
)

const uploadEndpoint = "/api/edge/upload"

type Client struct {
	TargetURL string
	Token     string
	SourceID  string
	MaxBytes  int64
	Client    *http.Client
}

type UploadResult struct {
	Success  bool   `json:"success"`
	File     string `json:"file"`
	Path     string `json:"path"`
	SourceID string `json:"source_id"`
	Hash     string `json:"hash"`
	Size     int64  `json:"size"`
	Records  int    `json:"records"`
	Message  string `json:"message"`
}

func (c Client) UploadFile(ctx context.Context, path string) (*UploadResult, error) {
	target, err := UploadURL(c.TargetURL)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat source file: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("source is a directory: %s", path)
	}
	if c.MaxBytes > 0 && info.Size() > c.MaxBytes {
		return nil, fmt.Errorf("source file is %d bytes, above max upload size %d", info.Size(), c.MaxBytes)
	}
	hash, err := filecopy.CalculateHash(path)
	if err != nil {
		return nil, fmt.Errorf("hash source file: %w", err)
	}

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, pr)
	if err != nil {
		_ = pr.Close()
		_ = pw.Close()
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("User-Agent", "patris-export-edge-stub")
	req.Header.Set("X-Patris-Source-ID", defaultSourceID(c.SourceID))
	req.Header.Set("X-Patris-File-Name", filepath.Base(path))
	req.Header.Set("X-Patris-File-Size", fmt.Sprintf("%d", info.Size()))
	req.Header.Set("X-Patris-File-CRC32", hash)
	if token := strings.TrimSpace(c.Token); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	writeErr := make(chan error, 1)
	go func() {
		defer close(writeErr)
		defer pw.Close()

		fields := map[string]string{
			"source_id":   defaultSourceID(c.SourceID),
			"source_path": path,
			"file_name":   filepath.Base(path),
			"size":        fmt.Sprintf("%d", info.Size()),
			"mod_time":    info.ModTime().UTC().Format(time.RFC3339Nano),
			"crc32":       hash,
		}
		for key, value := range fields {
			if err := mw.WriteField(key, value); err != nil {
				writeErr <- err
				_ = mw.Close()
				return
			}
		}

		part, err := mw.CreateFormFile("file", filepath.Base(path))
		if err != nil {
			writeErr <- err
			_ = mw.Close()
			return
		}
		file, err := os.Open(path)
		if err != nil {
			writeErr <- err
			_ = mw.Close()
			return
		}
		defer file.Close()
		if _, err := io.CopyBuffer(part, file, make([]byte, filecopy.ChunkSize)); err != nil {
			writeErr <- err
			_ = mw.Close()
			return
		}
		writeErr <- mw.Close()
	}()

	httpClient := c.Client
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Minute}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		_ = pr.CloseWithError(err)
		_ = pw.CloseWithError(err)
		<-writeErr
		return nil, err
	}
	defer resp.Body.Close()

	if err := <-writeErr; err != nil {
		return nil, fmt.Errorf("stream upload body: %w", err)
	}

	var result UploadResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode upload response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if result.Message != "" {
			return &result, fmt.Errorf("upload failed: HTTP %d: %s", resp.StatusCode, result.Message)
		}
		return &result, fmt.Errorf("upload failed: HTTP %d", resp.StatusCode)
	}
	return &result, nil
}

func UploadURL(target string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", fmt.Errorf("edge target URL is required")
	}
	u, err := url.Parse(target)
	if err != nil {
		return "", fmt.Errorf("parse edge target URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("edge target URL must use http or https")
	}
	if u.Host == "" {
		return "", fmt.Errorf("edge target URL must include a host")
	}
	if strings.TrimRight(u.Path, "/") == uploadEndpoint {
		return u.String(), nil
	}
	u.Path = strings.TrimRight(u.Path, "/") + uploadEndpoint
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func defaultSourceID(value string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	if host, err := os.Hostname(); err == nil && strings.TrimSpace(host) != "" {
		return host
	}
	return "edge"
}
