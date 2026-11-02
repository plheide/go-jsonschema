package generator

import (
	"strings"

	"github.com/plheide/go-jsonschema/pkg/schemas"
)

// deprecationPrefix starts the doc comment paragraph that gopls and
// staticcheck look for to report the uses of a deprecated identifier.
const deprecationPrefix = "Deprecated: "

// withDeprecation returns comment, the doc comment of a type or field generated
// from t, with a "Deprecated:" paragraph added when t's deprecated keyword
// marks it, as withDeprecationOf does.
func withDeprecation(comment string, t *schemas.Type) string {
	if t == nil {
		return comment
	}

	return withDeprecationOf(comment, t.Deprecated)
}

// withDeprecationOf returns comment with a "Deprecated:" paragraph added when
// the deprecated keyword d marks it. The paragraph is the keyword's text when
// it is a string. A comment that has such a paragraph already is kept as it is.
func withDeprecationOf(comment string, d schemas.Deprecation) string {
	if !d.Marked {
		return comment
	}

	for line := range strings.SplitSeq(comment, "\n") {
		if strings.HasPrefix(line, deprecationPrefix) {
			return comment
		}
	}

	paragraph := deprecationPrefix + "The JSON schema marks this as deprecated."
	if message := strings.TrimSpace(d.Message); message != "" {
		paragraph = deprecationPrefix + strings.TrimPrefix(message, deprecationPrefix)
	}

	if comment == "" {
		return paragraph
	}

	return comment + "\n\n" + paragraph
}
