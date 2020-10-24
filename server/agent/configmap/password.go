package configmap

import (
	"regexp"
)

var (
	short = regexp.MustCompile(`(.{1})(.+)(.{1})`)
	long  = regexp.MustCompile(`(.{2})(.+)(.{2})`)
)

func censor(s string) string {
	switch l := len(s); {
	case l <= 3:
		return s
	case l <= 5:
		return short.ReplaceAllString(s, "$1*$3")
	default:
		return long.ReplaceAllString(s, "$1**$3")
	}
}
