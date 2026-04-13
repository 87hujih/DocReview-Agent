package parser

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

const (
	// ModeText 表示仅支持纯文本文件直通。
	ModeText = "text"
	// ModeTika 表示文本文件直通，其他受支持文档走 Tika 解析。
	ModeTika = "tika"

	defaultTikaTimeout = 30 * time.Second
)

var (
	// ErrUnsupportedFileType 表示当前文件扩展名不在支持范围内。
	ErrUnsupportedFileType = errors.New("不支持的文件格式")
)

// Input 描述一次文档解析请求。
type Input struct {
	FileName string
	Content  []byte
}

// Result 描述文档解析后的文本正文。
type Result struct {
	Text string
}

// Parser 定义文档解析器的统一入口。
type Parser interface {
	Parse(ctx context.Context, input Input) (*Result, error)
}

// Options 描述构造解析器所需的可选参数。
type Options struct {
	Mode        string
	TikaURL     string
	TikaTimeout time.Duration
	HTTPClient  *http.Client
}

type documentParser struct {
	text *textParser
	tika *tikaParser
}

// New 根据模式构造解析器。
func New(options Options) (Parser, error) {
	mode := strings.TrimSpace(strings.ToLower(options.Mode))
	if mode == "" {
		mode = ModeText
	}

	parser := &documentParser{
		text: &textParser{},
	}

	switch mode {
	case ModeText:
		return parser, nil
	case ModeTika:
		timeout := options.TikaTimeout
		if timeout <= 0 {
			timeout = defaultTikaTimeout
		}

		if strings.TrimSpace(options.TikaURL) == "" {
			return nil, errors.New("Tika URL 不能为空")
		}

		parser.tika = &tikaParser{
			baseURL: strings.TrimRight(options.TikaURL, "/"),
			client:  chooseHTTPClient(options.HTTPClient, timeout),
		}
		return parser, nil
	default:
		return nil, fmt.Errorf("不支持的文档解析模式：%s", mode)
	}
}

func (p *documentParser) Parse(ctx context.Context, input Input) (*Result, error) {
	extension := normalizeExtension(input.FileName)

	switch {
	case isTextExtension(extension):
		return p.text.Parse(ctx, input)
	case isTikaExtension(extension):
		if p.tika == nil {
			return nil, fmt.Errorf("%w：%s 需要启用 Tika 解析", ErrUnsupportedFileType, extension)
		}
		return p.tika.Parse(ctx, input)
	default:
		return nil, fmt.Errorf("%w：%s", ErrUnsupportedFileType, extension)
	}
}

// IsSupportedFileName 返回当前首期支持的文件名是否可被解析链路接受。
func IsSupportedFileName(fileName string) bool {
	extension := normalizeExtension(fileName)
	return isTextExtension(extension) || isTikaExtension(extension)
}

func normalizeExtension(fileName string) string {
	extension := strings.ToLower(strings.TrimSpace(filepath.Ext(fileName)))
	if extension == "" {
		return "(无扩展名)"
	}

	return extension
}

func isTextExtension(extension string) bool {
	switch extension {
	case ".md", ".txt":
		return true
	default:
		return false
	}
}

func isTikaExtension(extension string) bool {
	switch extension {
	case ".doc", ".docx", ".pdf", ".rtf", ".odt":
		return true
	default:
		return false
	}
}

func chooseHTTPClient(client *http.Client, timeout time.Duration) *http.Client {
	if client == nil {
		return &http.Client{Timeout: timeout}
	}

	cloned := *client
	if cloned.Timeout <= 0 {
		cloned.Timeout = timeout
	}

	return &cloned
}
