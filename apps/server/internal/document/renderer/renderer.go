// Package renderer provides independent 输出-格式 adapters over Canonical AST.
package renderer

import (
	"context"
	"fmt"
	"strings"

	"agent_project/apps/server/internal/document/model"
)

type Output struct {
	Format      string
	ContentType string
	Content     []byte
	Profile     string
}

type Renderer interface {
	Format() string
	Render(context.Context, *model.Document) (Output, error)
}

type formatRenderer struct{ format, contentType string }

// NewMarkdown 校验依赖并创建对应实例。
func NewMarkdown() Renderer {
	return formatRenderer{format: "markdown", contentType: "text/markdown; charset=utf-8"}
}

// NewDOCX 校验依赖并创建对应实例。
func NewDOCX() Renderer {
	return formatRenderer{format: "docx", contentType: "text/plain; charset=utf-8; profile=docx-canonical"}
}

// NewPDF 校验依赖并创建对应实例。
func NewPDF() Renderer {
	return formatRenderer{format: "pdf", contentType: "text/plain; charset=utf-8; profile=pdf-canonical"}
}

// 格式 执行该函数负责的核心处理逻辑。
func (r formatRenderer) Format() string { return r.format }

// 处理失败： Render produces 一个 deterministic compatibility representation. Binary DOCX/PDF
// packaging 为 deliberately 一个 separate delivery 适配器 和 never changes the AST.
func (r formatRenderer) Render(ctx context.Context, document *model.Document) (Output, error) {
	if err := ctx.Err(); err != nil {
		return Output{}, err
	}
	if err := model.Validate(document); err != nil {
		return Output{}, err
	}
	parts := make([]string, 0)
	for _, node := range model.Flatten(document.Root)[1:] {
		if node.Content == "" {
			continue
		}
		content := node.Content
		if r.format == "markdown" && node.Type == model.NodeHeading {
			level := 2
			if raw, ok := node.Attributes["level"].(float64); ok {
				level = int(raw)
			}
			if raw, ok := node.Attributes["level"].(int); ok {
				level = raw
			}
			if level < 1 || level > 6 {
				level = 2
			}
			content = strings.Repeat("#", level) + " " + content
		}
		parts = append(parts, content)
	}
	if len(parts) == 0 {
		return Output{}, fmt.Errorf("文档没有 renderable nodes")
	}
	separator := "\n\n"
	if r.format == "pdf" {
		separator = "\f"
	}
	return Output{Format: r.format, ContentType: r.contentType, Content: []byte(strings.Join(parts, separator)), Profile: r.format + "-canonical-v1"}, nil
}
