package builtin

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type fileMemoryBackend struct {
	mu   sync.Mutex
	path string
}

func NewFileMemoryBackend(path string) MemoryBackend {
	return &fileMemoryBackend{path: path}
}

func (b *fileMemoryBackend) Save(_ context.Context, key, value string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	data, err := b.read()
	if err != nil {
		return err
	}
	data[key] = value
	return b.write(data)
}

func (b *fileMemoryBackend) Forget(_ context.Context, key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	data, err := b.read()
	if err != nil {
		return err
	}
	delete(data, key)
	return b.write(data)
}

func (b *fileMemoryBackend) List(context.Context) (map[string]string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.read()
}

func (b *fileMemoryBackend) Clear(context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.write(map[string]string{})
}

func (b *fileMemoryBackend) read() (map[string]string, error) {
	result := map[string]string{}
	f, err := os.Open(b.path)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		item := strings.TrimSpace(strings.TrimPrefix(line, "- "))
		key, value, ok := strings.Cut(item, ":")
		if !ok {
			key, value, ok = strings.Cut(item, "：")
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if ok && key != "" && value != "" {
			result[key] = value
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (b *fileMemoryBackend) write(data map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(b.path), 0755); err != nil {
		return err
	}
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	sb.WriteString("# Memory\n\n")
	if len(keys) == 0 {
		sb.WriteString("No saved memories yet.\n")
	} else {
		for _, k := range keys {
			fmt.Fprintf(&sb, "- %s: %s\n", k, strings.TrimSpace(data[k]))
		}
	}
	tmp := b.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(sb.String()), 0600); err != nil {
		return err
	}
	return os.Rename(tmp, b.path)
}
