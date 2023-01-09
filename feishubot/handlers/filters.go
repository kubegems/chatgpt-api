package handlers

import (
	"os"
	"strings"
)

// 过滤一些关键词, 某些情况下，有些词或句子不能出现，不要问我为啥
type KeywordsFilter struct {
	words []string
}

func (f *KeywordsFilter) Filter(text string) string {
	for _, key := range f.words {
		if strings.Contains(text, key) {
			text := os.Getenv(FilterReplaceText)
			if text == "" {
				return "..."
			}
			return text
		}
	}
	return text
}

var predefinedFilters map[string]MessageFilter

func init() {
	predefinedFilters = map[string]MessageFilter{
		"keywords": &KeywordsFilter{
			words: strings.Split(os.Getenv(ProhibitedWords), ","),
		},
	}
}
