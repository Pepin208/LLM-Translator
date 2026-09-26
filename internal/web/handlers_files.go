package web

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
)

// --- Upload / download -----------------------------------------------------

const (
	maxUploadFiles = 50
	maxUploadBytes = 25 * 1024 * 1024
)

var allowedExts = map[string]struct{}{".srt": {}, ".ass": {}, ".vtt": {}}

func (s *Server) handleUpload(w http.ResponseWriter, req *http.Request) {
	if err := req.ParseMultipartForm(maxUploadBytes); err != nil {
		s.logger.Warn("upload rejected: invalid multipart form", "error", err, "ip", clientIP(req))
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "Invalid multipart form."})
		return
	}
	files := req.MultipartForm.File["files"]
	if len(files) > maxUploadFiles {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": fmt.Sprintf("Too many files. Maximum is %d per upload.", maxUploadFiles)})
		return
	}

	type uploaded struct {
		Filename     string `json:"filename"`
		OriginalName string `json:"original_name"`
		Filepath     string `json:"filepath"`
		Size         int64  `json:"size"`
	}
	var result []uploaded

	for _, fh := range files {
		ext := strings.ToLower(filepath.Ext(fh.Filename))
		if _, ok := allowedExts[ext]; !ok {
			writeJSON(w, http.StatusBadRequest, map[string]any{"detail": fmt.Sprintf("Unsupported file format '%s'. Only .srt, .ass, and .vtt files are allowed.", ext)})
			return
		}
		safe := filepath.Base(fh.Filename)
		targetName := uuid8() + "_" + safe
		targetPath := filepath.Join(s.uploadDir, targetName)

		src, err := fh.Open()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": "Failed to read upload."})
			return
		}
		dst, err := os.Create(targetPath)
		if err != nil {
			src.Close()
			writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": "Failed to store upload."})
			return
		}
		written, err := io.Copy(dst, io.LimitReader(src, maxUploadBytes+1))
		src.Close()
		dst.Close()
		if err != nil {
			os.Remove(targetPath)
			writeJSON(w, http.StatusInternalServerError, map[string]any{"detail": "Failed to store upload."})
			return
		}
		if written > maxUploadBytes {
			os.Remove(targetPath)
			s.logger.Warn("upload rejected: file too large", "filename", safe, "bytes", written)
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"detail": fmt.Sprintf("'%s' exceeds the %d MB limit.", safe, maxUploadBytes/(1024*1024))})
			return
		}

		s.logger.Info("file uploaded", "filename", safe, "stored_as", targetName, "bytes", written, "ip", clientIP(req))
		result = append(result, uploaded{targetName, safe, targetPath, written})
	}
	writeJSON(w, http.StatusOK, map[string]any{"uploaded_files": result})
}

func (s *Server) handleDownload(w http.ResponseWriter, req *http.Request) {
	safe := filepath.Base(chi.URLParam(req, "filename"))
	if safe == "" || safe == "." || safe == ".." {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "Invalid filename."})
		return
	}

	dirs := []string{s.outputDir, s.uploadDir, s.baseDir}
	for _, d := range dirs {
		target := filepath.Join(d, safe)
		if isWithin(d, target) {
			if info, err := os.Stat(target); err == nil && !info.IsDir() {
				s.logger.Debug("download served", "filename", safe, "path", target)
				serveDownload(w, req, target, userFacingName(safe))
				return
			}
		}
	}

	suffix := "_" + safe
	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), suffix) {
				continue
			}
			s.logger.Debug("download served", "filename", e.Name(), "dir", d)
			serveDownload(w, req, filepath.Join(d, e.Name()), userFacingName(e.Name()))
			return
		}
	}
	s.logger.Warn("download not found", "filename", safe)
	writeJSON(w, http.StatusNotFound, map[string]any{"detail": fmt.Sprintf("File '%s' not found.", safe)})
}

var uuidPrefix = regexp.MustCompile(`^[0-9a-fA-F]{8}_`)

func uuid8() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func userFacingName(name string) string {
	return uuidPrefix.ReplaceAllString(name, "")
}

func isWithin(dir, target string) bool {
	rel, err := filepath.Rel(dir, target)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)
}

func serveDownload(w http.ResponseWriter, req *http.Request, path, filename string) {
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, req, path)
}
