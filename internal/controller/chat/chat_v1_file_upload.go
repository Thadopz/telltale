package chat

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/Thadopz/telltale/api/chat/v1"
	"github.com/Thadopz/telltale/internal/ai/agent/knowledge_index_pipeline"
	loader2 "github.com/Thadopz/telltale/internal/ai/loader"
	"github.com/Thadopz/telltale/internal/ai/vectorstore"
	"github.com/Thadopz/telltale/utility/common"

	"github.com/cloudwego/eino/components/document"
	"github.com/gogf/gf/v2/errors/gerror"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/os/gfile"
)

const defaultMaxUploadBytes int64 = 4 * 1024 * 1024

var allowedUploadExtensions = map[string]bool{
	".csv":      true,
	".json":     true,
	".log":      true,
	".markdown": true,
	".md":       true,
	".sql":      true,
	".txt":      true,
	".yaml":     true,
	".yml":      true,
}

func (c *ControllerV1) FileUpload(ctx context.Context, req *v1.FileUploadReq) (res *v1.FileUploadRes, err error) {
	r := g.RequestFromCtx(ctx)
	uploadFile := r.GetUploadFile("file")
	if uploadFile == nil {
		return nil, gerror.New("file is required")
	}
	maxBytes := configuredMaxUploadBytes(ctx)
	if err := validateUploadMetadata(uploadFile, maxBytes); err != nil {
		return nil, err
	}

	if !gfile.Exists(common.FileDir) {
		if err := gfile.Mkdir(common.FileDir); err != nil {
			return nil, gerror.Wrapf(err, "create upload directory failed: %s", common.FileDir)
		}
	}
	savedName, err := uploadFile.Save(common.FileDir, true)
	if err != nil {
		return nil, gerror.Wrap(err, "save uploaded file failed")
	}
	filePath := filepath.Join(common.FileDir, savedName)
	keepFile := false
	defer func() {
		if !keepFile {
			_ = os.Remove(filePath)
		}
	}()

	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return nil, gerror.Wrap(err, "stat uploaded file failed")
	}
	if err = validateUploadedTextFile(filePath, maxBytes); err != nil {
		return nil, err
	}
	if err = buildIntoIndex(ctx, filePath); err != nil {
		return nil, gerror.Wrap(err, "build sqlite knowledge index failed")
	}
	keepFile = true
	return &v1.FileUploadRes{
		FileName: savedName,
		FilePath: filePath,
		FileSize: fileInfo.Size(),
	}, nil
}

func configuredMaxUploadBytes(ctx context.Context) int64 {
	value, err := g.Cfg().Get(ctx, "uploads.max_file_bytes")
	if err != nil || value == nil || value.Int64() <= 0 {
		return defaultMaxUploadBytes
	}
	return value.Int64()
}

func validateUploadMetadata(uploadFile *ghttp.UploadFile, maxBytes int64) error {
	if uploadFile == nil || uploadFile.FileHeader == nil {
		return fmt.Errorf("file is required")
	}
	name := strings.TrimSpace(uploadFile.Filename)
	if name == "" || strings.ContainsAny(name, "\x00\r\n") {
		return fmt.Errorf("uploaded file name is invalid")
	}
	normalizedName := strings.ReplaceAll(name, "\\", "/")
	if path.Base(normalizedName) != normalizedName || filepath.Base(name) != name {
		return fmt.Errorf("uploaded file name must not contain a path")
	}
	extension := strings.ToLower(filepath.Ext(name))
	if !allowedUploadExtensions[extension] {
		return fmt.Errorf("uploaded file type %q is not allowed", extension)
	}
	if uploadFile.Size <= 0 {
		return fmt.Errorf("uploaded file is empty")
	}
	if maxBytes <= 0 {
		maxBytes = defaultMaxUploadBytes
	}
	if uploadFile.Size > maxBytes {
		return fmt.Errorf("uploaded file exceeds the %d byte limit", maxBytes)
	}
	return nil
}

func validateUploadedTextFile(filePath string, maxBytes int64) error {
	if maxBytes <= 0 {
		maxBytes = defaultMaxUploadBytes
	}
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open uploaded file: %w", err)
	}
	defer file.Close()

	content, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return fmt.Errorf("read uploaded file: %w", err)
	}
	if int64(len(content)) > maxBytes {
		return fmt.Errorf("uploaded file exceeds the %d byte limit", maxBytes)
	}
	if len(content) == 0 {
		return fmt.Errorf("uploaded file is empty")
	}
	if bytes.IndexByte(content, 0) >= 0 || !utf8.Valid(content) {
		return fmt.Errorf("uploaded file must contain UTF-8 text")
	}
	return nil
}

func buildIntoIndex(ctx context.Context, path string) error {
	runner, err := knowledge_index_pipeline.BuildKnowledgeIndexing(ctx)
	if err != nil {
		return err
	}
	loader, err := loader2.NewFileLoader(ctx)
	if err != nil {
		return err
	}
	docs, err := loader.Load(ctx, document.Source{URI: path})
	if err != nil {
		return err
	}
	if len(docs) == 0 {
		return fmt.Errorf("document loader returned no documents")
	}
	if source := fmt.Sprint(docs[0].MetaData["_source"]); source != "" {
		if err = vectorstore.DeleteKnowledgeBySource(ctx, source); err != nil {
			return fmt.Errorf("delete existing sqlite knowledge records: %w", err)
		}
	}
	ids, err := runner.Invoke(ctx, document.Source{URI: path})
	if err != nil {
		return fmt.Errorf("invoke index graph failed: %w", err)
	}
	fmt.Printf("[done] indexing file: %s, parts: %d\n", path, len(ids))
	return nil
}
