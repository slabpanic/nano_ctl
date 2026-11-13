package httpapi

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"nano_ctl/service/store"
)

func readBusConfigUpload(c *gin.Context, kind string) ([]byte, error) {
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		return nil, invalid("multipart field 'file' is required")
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if kind == "eni" && ext != ".eni" && ext != ".xml" {
		return nil, invalid("ENI upload must use .eni or .xml extension")
	}
	if kind == "ini" && ext != ".ini" {
		return nil, invalid("INI upload must use .ini extension")
	}
	limit := int64(store.MaxENIFileSize)
	if kind == "ini" {
		limit = int64(store.MaxINIFileSize)
	}
	if header.Size > limit {
		return nil, invalid(fmt.Sprintf("%s file exceeds %d MiB", strings.ToUpper(kind), limit>>20))
	}
	content, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, invalid("unable to read uploaded file")
	}
	if int64(len(content)) > limit {
		return nil, invalid(fmt.Sprintf("%s file exceeds %d MiB", strings.ToUpper(kind), limit>>20))
	}
	if len(content) == 0 {
		return nil, invalid("uploaded file is empty")
	}
	return content, nil
}
