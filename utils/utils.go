package utils

import (
	"fmt"
	"strings"
)

func UrlQuery(s string) string { return strings.ReplaceAll(s, " ", "+") }

func Str(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}
