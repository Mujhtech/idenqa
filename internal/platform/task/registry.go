package task

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"sync"
)

// Registry maps exact durable task versions to handlers. It supports rolling
// deployments by allowing adjacent payload versions to coexist explicitly.
type Registry struct {
	mutex    sync.RWMutex
	handlers map[Key]Handler
}

// NewRegistry constructs an empty registry.
func NewRegistry() *Registry {
	return &Registry{handlers: make(map[Key]Handler)}
}

// Register installs one exact task version and rejects ambiguous replacement.
func (registry *Registry) Register(key Key, handler Handler) error {
	if registry == nil || key.Validate() != nil || handler == nil {
		return fmt.Errorf("%w: registry entry", ErrInvalid)
	}
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if _, exists := registry.handlers[key]; exists {
		return fmt.Errorf("%w: task handler %s v%d", ErrConflict, key.Name, key.Version)
	}
	registry.handlers[key] = handler
	return nil
}

// Resolve returns the exact compatible handler. It never guesses or falls
// forward to a different payload version.
func (registry *Registry) Resolve(key Key) (Handler, error) {
	if registry == nil || key.Validate() != nil {
		return nil, fmt.Errorf("%w: registry lookup", ErrInvalid)
	}
	registry.mutex.RLock()
	defer registry.mutex.RUnlock()
	handler, exists := registry.handlers[key]
	if !exists {
		return nil, errors.Join(ErrNotFound, fmt.Errorf("task handler %s v%d", key.Name, key.Version))
	}
	return handler, nil
}

// Keys returns a defensive, stable snapshot of registered task versions.
func (registry *Registry) Keys() []Key {
	if registry == nil {
		return nil
	}
	registry.mutex.RLock()
	defer registry.mutex.RUnlock()
	keys := make([]Key, 0, len(registry.handlers))
	for key := range registry.handlers {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(left, right Key) int {
		if comparison := cmp.Compare(left.Name, right.Name); comparison != 0 {
			return comparison
		}
		return cmp.Compare(left.Version, right.Version)
	})
	return keys
}

// HasName reports whether any explicitly supported version of name exists.
// Adapters use it to distinguish a rolling deploy missing an entire handler
// from a poison payload version for a handler that is present.
func (registry *Registry) HasName(name Name) bool {
	if registry == nil {
		return false
	}
	registry.mutex.RLock()
	defer registry.mutex.RUnlock()
	for key := range registry.handlers {
		if key.Name == name {
			return true
		}
	}
	return false
}
