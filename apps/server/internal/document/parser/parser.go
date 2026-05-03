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

	textExtensions = []string{".md", ".txt"}
	tikaExtensions = []string{".doc", ".docx", ".pdf", ".rtf", ".odt"}
)

// Input 描述一次文档解析请求。
type Input struct {
	FileName string
	Content  []byte
}

// Result 描述文档解析后的 canonical text 与结构化结果。
type Result struct {
	Text     string
	Document *ParsedDocument
}

// Parser 定义文档解析器的统一入口。
type Parser interface {
	Parse(ctx context.Context, input Input) (*Result, error)
	SupportsFileName(fileName string) bool
	SupportedExtensions() []string
	UnsupportedFileMessage(fileName string) string
}

// Options 描述构造解析器所需的可选参数。
type Options struct {
	Mode        string
	TikaURL     string
	TikaTimeout time.Duration
	HTTPClient  *http.Client
}

// documentParser 承载文档解析器相关状态，明确文档解析链路中的数据边界。
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

// Parse 解析 `Parse`，统一输入校验和领域结果构造。
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

// SupportsFileName 判断给定文件名是否在当前解析能力支持的扩展名集合里。
func (p *documentParser) SupportsFileName(fileName string) bool {
	extension := normalizeExtension(fileName)
	if isTextExtension(extension) {
		return true
	}

	return p.tika != nil && isTikaExtension(extension)
}

// SupportedExtensions 返回当前文档解析能力支持的扩展名列表，保持前端展示与后端校验一致。
func (p *documentParser) SupportedExtensions() []string {
	extensions := append([]string(nil), textExtensions...)
	if p.tika != nil {
		extensions = append(extensions, tikaExtensions...)
	}

	return extensions
}

// UnsupportedFileMessage 为不受支持的文档类型生成提示文案，统一解析入口返回的错误口径。
func (p *documentParser) UnsupportedFileMessage(fileName string) string {
	extension := normalizeExtension(fileName)
	if p.tika == nil && isTikaExtension(extension) {
		return "当前服务仅支持 md、txt；pdf/docx 等文件需要启用 Tika 解析。"
	}

	return fmt.Sprintf("不支持的文件格式：%s。当前支持：%s。", extension, formatExtensions(p.SupportedExtensions()))
}

// normalizeExtension 归一化 `扩展名`，避免后续流程重复处理边界输入。
func normalizeExtension(fileName string) string {
	extension := strings.ToLower(strings.TrimSpace(filepath.Ext(fileName)))
	if extension == "" {
		return "(无扩展名)"
	}

	return extension
}

// isTextExtension 判断文件扩展名是否属于纯文本解析能力支持的集合。
func isTextExtension(extension string) bool {
	return containsExtension(textExtensions, extension)
}

// isTikaExtension 判断文件扩展名是否属于 Tika 解析链路支持的集合。
func isTikaExtension(extension string) bool {
	return containsExtension(tikaExtensions, extension)
}

// containsExtension 判断扩展名列表里是否包含目标后缀，统一大小写与点号差异处理。
func containsExtension(extensions []string, extension string) bool {
	for _, supported := range extensions {
		if extension == supported {
			return true
		}
	}

	return false
}

// formatExtensions 把扩展名列表格式化成提示文案，供错误消息和能力展示复用。
func formatExtensions(extensions []string) string {
	names := make([]string, 0, len(extensions))
	for _, extension := range extensions {
		names = append(names, strings.TrimPrefix(extension, "."))
	}

	return strings.Join(names, "、")
}

// chooseHTTPClient 根据配置选择直连或带超时约束的 HTTP 客户端，供文档解析链路复用。
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
