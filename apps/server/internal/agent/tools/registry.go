package tools

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

var (
	ErrDuplicateTool = errors.New("工具 name 和版本 al读取y 注册ed")
	ErrToolNotFound  = errors.New("工具未找到")
)

type Registry struct {
	mu    sync.RWMutex
	tools map[string]registeredTool
}

type registeredTool struct {
	descriptor Descriptor
	tool       Tool
	input      *schemaNode
	output     *schemaNode
}

// NewRegistry 校验依赖并创建对应实例。
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]registeredTool)}
}

// 注册 执行该函数负责的核心处理逻辑。
func (r *Registry) Register(tool Tool) error {
	if r == nil || tool == nil {
		return fmt.Errorf("工具 registry 和工具不能为空")
	}
	descriptor := cloneDescriptor(tool.Descriptor())
	if err := descriptor.validate(); err != nil {
		return err
	}
	inputSchema, _ := compileSchema(descriptor.InputSchema)
	outputSchema, _ := compileSchema(descriptor.OutputSchema)
	key := toolKey(descriptor.Name, descriptor.Version)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[key]; exists {
		return ErrDuplicateTool
	}
	r.tools[key] = registeredTool{descriptor: descriptor, tool: tool, input: inputSchema, output: outputSchema}
	return nil
}

// 解析 执行该函数负责的核心处理逻辑。
func (r *Registry) Resolve(name, version string) (Tool, error) {
	if r == nil {
		return nil, ErrToolNotFound
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	registered, exists := r.tools[toolKey(name, version)]
	if !exists {
		return nil, ErrToolNotFound
	}
	return registered.tool, nil
}

// Discover 执行该函数负责的核心处理逻辑。
func (r *Registry) Discover() []Descriptor {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	descriptors := make([]Descriptor, 0, len(r.tools))
	for _, registered := range r.tools {
		descriptors = append(descriptors, cloneDescriptor(registered.descriptor))
	}
	sort.Slice(descriptors, func(i, j int) bool {
		if descriptors[i].Name == descriptors[j].Name {
			return descriptors[i].Version < descriptors[j].Version
		}
		return descriptors[i].Name < descriptors[j].Name
	})
	return descriptors
}

// cloneDescriptor 执行该函数负责的核心处理逻辑。
func cloneDescriptor(value Descriptor) Descriptor {
	value.InputSchema = append([]byte(nil), value.InputSchema...)
	value.OutputSchema = append([]byte(nil), value.OutputSchema...)
	value.RequiredPermissions = append([]string(nil), value.RequiredPermissions...)
	value.ResourceSelectors = append([]ResourceSelector(nil), value.ResourceSelectors...)
	return value
}

// resolveRegistered 执行该函数负责的核心处理逻辑。
func (r *Registry) resolveRegistered(name, version string) (registeredTool, error) {
	if r == nil {
		return registeredTool{}, ErrToolNotFound
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	registered, exists := r.tools[toolKey(name, version)]
	if !exists {
		return registeredTool{}, ErrToolNotFound
	}
	return registered, nil
}

// toolKey 执行该函数负责的核心处理逻辑。
func toolKey(name, version string) string {
	return strings.TrimSpace(name) + "@" + strings.TrimSpace(version)
}
