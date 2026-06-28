package app

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

func normalizeRow(row map[string]any) {
	for key, value := range row {
		switch typed := value.(type) {
		case []byte:
			var decoded any
			if json.Unmarshal(typed, &decoded) == nil {
				row[key] = sanitizeRowValue(key, decoded)
			} else {
				row[key] = sanitizeRowValue(key, string(typed))
			}
		case time.Time:
			row[key] = typed.Format(time.RFC3339)
		default:
			row[key] = sanitizeRowValue(key, value)
		}
	}
}

func sanitizeRowValue(key string, value any) any {
	return sanitizeAnyValue(key, value)
}

func sanitizeMetadata(metadata map[string]any) map[string]any {
	if metadata == nil {
		return map[string]any{}
	}
	sanitized, _ := sanitizeAnyValue("metadata", metadata).(map[string]any)
	return sanitized
}

func sanitizeAnyValue(key string, value any) any {
	if isSensitiveMetadataKey(key) {
		return "<redacted>"
	}
	switch typed := value.(type) {
	case string:
		return sanitizeURLUserInfo(typed)
	case map[string]any:
		out := make(map[string]any, len(typed))
		for nestedKey, nestedValue := range typed {
			out[nestedKey] = sanitizeAnyValue(nestedKey, nestedValue)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, sanitizeAnyValue("", item))
		}
		return out
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			out = append(out, sanitizeURLUserInfo(item))
		}
		return out
	default:
		return value
	}
}

func isSensitiveMetadataKey(key string) bool {
	key = strings.ToLower(key)
	// These suffixes represent boolean or finite-enum readiness metadata.
	// Values with secret material must not be stored under these field names.
	if strings.HasSuffix(key, "_present") ||
		strings.HasSuffix(key, "_configured") ||
		strings.HasSuffix(key, "_ready") ||
		strings.HasSuffix(key, "_status") ||
		strings.HasSuffix(key, "_state") {
		return false
	}
	return strings.Contains(key, "token") ||
		strings.Contains(key, "secret") ||
		strings.Contains(key, "password") ||
		strings.Contains(key, "private_key") ||
		strings.Contains(key, "credential")
}

var urlUserInfoPattern = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)[^/\s@]+@`)

func sanitizeURLUserInfo(value string) string {
	return urlUserInfoPattern.ReplaceAllString(value, "${1}<redacted>@")
}
