package render

import (
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"text/template"
	"time"
)

func (e *Engine) funcMap() template.FuncMap {
	return template.FuncMap{
		"basename":       filepath.Base,
		"dirname":        filepath.Dir,
		"cleanPath":      filepath.Clean,
		"homeRelative":   homeRelative,
		"default":        defaultValue,
		"coalesce":       coalesce,
		"lower":          strings.ToLower,
		"upper":          strings.ToUpper,
		"title":          strings.Title,
		"trim":           strings.TrimSpace,
		"replace":        replace,
		"contains":       contains,
		"hasPrefix":      hasPrefix,
		"hasSuffix":      hasSuffix,
		"matches":        matches,
		"join":           join,
		"first":          first,
		"last":           last,
		"truncate":       truncate,
		"truncateMiddle": truncateMiddle,
		"padLeft":        padLeft,
		"padRight":       padRight,
		"alias":          e.alias,
		"statusIcon":     e.statusIcon,
		"formatTime":     formatTime,
	}
}

func cloneMaps(source map[string]map[string]string) map[string]map[string]string {
	if source == nil {
		return map[string]map[string]string{}
	}
	cloned := make(map[string]map[string]string, len(source))
	for name, entries := range source {
		copyEntries := make(map[string]string, len(entries))
		for key, value := range entries {
			copyEntries[key] = value
		}
		cloned[name] = copyEntries
	}
	return cloned
}

func homeRelative(value string) string {
	for _, root := range []string{"/Users/", "/home/"} {
		if !strings.HasPrefix(value, root) {
			continue
		}
		userAndPath := strings.TrimPrefix(value, root)
		user, relative, hasPath := strings.Cut(userAndPath, "/")
		if user == "" {
			return value
		}
		if !hasPath {
			return "~"
		}
		return "~/" + relative
	}
	return value
}

func defaultValue(fallback, value any) any {
	if isEmpty(value) {
		return fallback
	}
	return value
}

func coalesce(values ...any) any {
	for _, value := range values {
		if !isEmpty(value) {
			return value
		}
	}
	return ""
}

func isEmpty(value any) bool {
	if value == nil {
		return true
	}
	return reflect.ValueOf(value).IsZero()
}

func replace(old, new, value string) string { return strings.ReplaceAll(value, old, new) }

func contains(substr, value string) bool { return strings.Contains(value, substr) }

func hasPrefix(prefix, value string) bool { return strings.HasPrefix(value, prefix) }

func hasSuffix(suffix, value string) bool { return strings.HasSuffix(value, suffix) }

func matches(expression, value string) (bool, error) {
	matched, err := regexp.MatchString(expression, value)
	if err != nil {
		return false, fmt.Errorf("invalid regular expression %q: %w", expression, err)
	}
	return matched, nil
}

func join(separator string, values any) string {
	value := reflect.ValueOf(values)
	if !value.IsValid() || (value.Kind() != reflect.Array && value.Kind() != reflect.Slice) {
		return ""
	}
	items := make([]string, value.Len())
	for i := 0; i < value.Len(); i++ {
		items[i] = fmt.Sprint(value.Index(i).Interface())
	}
	return strings.Join(items, separator)
}

func first(values any) any { return collectionEnd(values, 0) }

func last(values any) any {
	value := reflect.ValueOf(values)
	if !value.IsValid() || (value.Kind() != reflect.Array && value.Kind() != reflect.Slice) {
		return ""
	}
	if value.Len() == 0 {
		return reflect.Zero(value.Type().Elem()).Interface()
	}
	return value.Index(value.Len() - 1).Interface()
}

func collectionEnd(values any, index int) any {
	value := reflect.ValueOf(values)
	if !value.IsValid() || (value.Kind() != reflect.Array && value.Kind() != reflect.Slice) {
		return ""
	}
	if value.Len() == 0 {
		return reflect.Zero(value.Type().Elem()).Interface()
	}
	return value.Index(index).Interface()
}

func (e *Engine) alias(group, value string) string {
	if replacement, ok := e.aliases[group][value]; ok {
		return replacement
	}
	return value
}

func (e *Engine) statusIcon(status string) string {
	if icon, ok := e.icons["agent_status"][status]; ok {
		return icon
	}
	return status
}

func formatTime(layout string, value time.Time) string { return value.Format(layout) }
