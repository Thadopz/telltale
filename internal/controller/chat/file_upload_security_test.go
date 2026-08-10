package chat

import (
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gogf/gf/v2/net/ghttp"
)

func TestValidateUploadMetadataRejectsMaliciousFiles(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		size     int64
	}{
		{name: "path traversal", filename: "../secrets.md", size: 10},
		{name: "windows traversal", filename: `..\secrets.md`, size: 10},
		{name: "executable", filename: "payload.exe", size: 10},
		{name: "empty", filename: "empty.md", size: 0},
		{name: "oversized", filename: "large.md", size: 101},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upload := &ghttp.UploadFile{FileHeader: &multipart.FileHeader{Filename: test.filename, Size: test.size}}
			if err := validateUploadMetadata(upload, 100); err == nil {
				t.Fatalf("expected upload to be rejected: %#v", test)
			}
		})
	}
}

func TestValidateUploadMetadataAcceptsBoundedText(t *testing.T) {
	upload := &ghttp.UploadFile{FileHeader: &multipart.FileHeader{Filename: "runbook.md", Size: 100}}
	if err := validateUploadMetadata(upload, 100); err != nil {
		t.Fatalf("expected text upload to be accepted: %v", err)
	}
}

func TestValidateUploadedTextFileRejectsBinaryAndOversizedContent(t *testing.T) {
	directory := t.TempDir()
	binaryPath := filepath.Join(directory, "binary.md")
	if err := os.WriteFile(binaryPath, []byte{'o', 'k', 0, 'x'}, 0o600); err != nil {
		t.Fatalf("write binary fixture: %v", err)
	}
	if err := validateUploadedTextFile(binaryPath, 100); err == nil {
		t.Fatal("expected binary content to be rejected")
	}

	largePath := filepath.Join(directory, "large.md")
	if err := os.WriteFile(largePath, []byte(strings.Repeat("x", 101)), 0o600); err != nil {
		t.Fatalf("write large fixture: %v", err)
	}
	if err := validateUploadedTextFile(largePath, 100); err == nil {
		t.Fatal("expected oversized content to be rejected")
	}
}
