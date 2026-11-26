// Package safeoutput strips terminal control sequences and private URL paths.
package safeoutput

import (
	"github.com/charmbracelet/x/ansi"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

var terminalURLs = regexp.MustCompile(`https?://[^\s<>]+`)

func Text(text string) string {
	text = ansi.Strip(text)
	text = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) || r == '\u202e' || r == '\u202d' {
			return -1
		}
		return r
	}, text)
	return terminalURLs.ReplaceAllStringFunc(text, func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil {
			return "[链接已隐藏]"
		}
		// Download/connection errors can include subscription tokens in paths.
		return u.Scheme + "://" + u.Host + "/…"
	})
}
