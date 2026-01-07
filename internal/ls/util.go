package ls

import (
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

type initOptions struct {
	SchemaPaths []string `json:"schemaPaths"`
}

func readInitializationOptions(options any) []string {
	if options == nil {
		return nil
	}

	data, err := json.Marshal(options)
	if err != nil {
		return nil
	}

	var decoded initOptions
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil
	}

	return decoded.SchemaPaths
}

func hasFileScheme(value string) bool {
	return strings.HasPrefix(value, "file://")
}

func uriToPath(uri protocol.DocumentUri) string {
	parsed, err := url.Parse(string(uri))
	if err != nil {
		return ""
	}
	if parsed.Scheme != "file" {
		return ""
	}
	path, err := url.PathUnescape(parsed.Path)
	if err != nil {
		return ""
	}
	return filepath.FromSlash(path)
}

func pathToURI(path string) protocol.DocumentUri {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return protocol.DocumentUri(path)
	}
	absPath = filepath.ToSlash(absPath)
	u := url.URL{
		Scheme: "file",
		Path:   absPath,
	}
	return protocol.DocumentUri(u.String())
}

func expandSchemaPattern(root, pattern string) []string {
	if pattern == "" {
		return nil
	}

	expanded := pattern
	if !filepath.IsAbs(expanded) && root != "" {
		expanded = filepath.Join(root, expanded)
	}

	if hasGlobMeta(expanded) {
		matches, err := filepath.Glob(expanded)
		if err != nil {
			return nil
		}
		return matches
	}

	return []string{expanded}
}

func hasGlobMeta(path string) bool {
	return strings.ContainsAny(path, "*?[")
}

func isGraphQLFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".graphql" || ext == ".graphqls"
}

func isSchemaPath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".graphqls" {
		return true
	}
	if ext != ".graphql" {
		return false
	}
	base := strings.ToLower(filepath.Base(path))
	return strings.Contains(base, "schema")
}

func isSchemaURI(uri protocol.DocumentUri) bool {
	path := uriToPath(uri)
	if path == "" {
		return false
	}
	return isSchemaPath(path)
}

func lineStartIndex(text string, line int) int {
	if line <= 1 {
		return 0
	}
	start := 0
	for i := 1; i < line; i++ {
		next := strings.Index(text[start:], "\n")
		if next == -1 {
			return len(text)
		}
		start += next + 1
	}
	return start
}
