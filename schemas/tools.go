// Package schemas exposes the frozen v1 tool schema definitions.
package schemas

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
)

//go:embed v1/*.schema.json v1/tools/*.schema.json
var toolFiles embed.FS

// ToolDefinition returns one self-contained request or result schema.
func ToolDefinition(name, definition string) (map[string]any, error) {
	if definition != "request" && definition != "result" {
		return nil, errors.New("unsupported tool schema definition")
	}
	resolver := schemaResolver{documents: map[string]map[string]any{}}
	file := fmt.Sprintf("v1/tools/%s.schema.json", name)
	document, err := resolver.document(file)
	if err != nil {
		return nil, err
	}
	definitions, ok := document["$defs"].(map[string]any)
	if !ok {
		return nil, errors.New("tool schema definitions unavailable")
	}
	schema, ok := definitions[definition].(map[string]any)
	if !ok {
		return nil, errors.New("tool schema definition unavailable")
	}
	resolved, err := resolver.resolve(schema, file, map[string]bool{})
	if err != nil {
		return nil, err
	}
	standalone, ok := resolved.(map[string]any)
	if !ok {
		return nil, errors.New("tool schema definition invalid")
	}
	return standalone, nil
}

type schemaResolver struct {
	documents map[string]map[string]any
}

func (resolver *schemaResolver) document(file string) (map[string]any, error) {
	file = path.Clean(file)
	if document := resolver.documents[file]; document != nil {
		return document, nil
	}
	data, err := toolFiles.ReadFile(file)
	if err != nil {
		return nil, errors.New("tool schema unavailable")
	}
	var document map[string]any
	if json.Unmarshal(data, &document) != nil {
		return nil, errors.New("tool schema invalid")
	}
	resolver.documents[file] = document
	return document, nil
}

func (resolver *schemaResolver) resolve(value any, currentFile string, stack map[string]bool) (any, error) {
	switch typed := value.(type) {
	case map[string]any:
		if reference, ok := typed["$ref"].(string); ok {
			resolved, err := resolver.resolveReference(reference, currentFile, stack)
			if err != nil {
				return nil, err
			}
			if len(typed) == 1 {
				return resolved, nil
			}
			object, ok := resolved.(map[string]any)
			if !ok {
				return nil, errors.New("referenced schema is not an object")
			}
			copy := cloneMap(object)
			for key, item := range typed {
				if key == "$ref" {
					continue
				}
				copy[key] = item
			}
			return resolver.resolve(copy, currentFile, stack)
		}
		copy := make(map[string]any, len(typed))
		for key, item := range typed {
			resolved, err := resolver.resolve(item, currentFile, stack)
			if err != nil {
				return nil, err
			}
			copy[key] = resolved
		}
		return copy, nil
	case []any:
		copy := make([]any, len(typed))
		for index, item := range typed {
			resolved, err := resolver.resolve(item, currentFile, stack)
			if err != nil {
				return nil, err
			}
			copy[index] = resolved
		}
		return copy, nil
	default:
		return value, nil
	}
}

func (resolver *schemaResolver) resolveReference(reference, currentFile string, stack map[string]bool) (any, error) {
	filePart, fragment, _ := strings.Cut(reference, "#")
	targetFile := currentFile
	if filePart != "" {
		targetFile = path.Clean(path.Join(path.Dir(currentFile), filePart))
	}
	key := targetFile + "#" + fragment
	if stack[key] {
		return nil, errors.New("cyclic tool schema reference")
	}
	document, err := resolver.document(targetFile)
	if err != nil {
		return nil, err
	}
	var target any = document
	if fragment != "" {
		if !strings.HasPrefix(fragment, "/") {
			return nil, errors.New("unsupported tool schema reference")
		}
		for _, part := range strings.Split(strings.TrimPrefix(fragment, "/"), "/") {
			part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
			object, ok := target.(map[string]any)
			if !ok {
				return nil, errors.New("tool schema reference invalid")
			}
			target, ok = object[part]
			if !ok {
				return nil, errors.New("tool schema reference unavailable")
			}
		}
	}
	next := make(map[string]bool, len(stack)+1)
	for item := range stack {
		next[item] = true
	}
	next[key] = true
	return resolver.resolve(target, targetFile, next)
}

func cloneMap(value map[string]any) map[string]any {
	copy := make(map[string]any, len(value))
	for key, item := range value {
		copy[key] = item
	}
	return copy
}
