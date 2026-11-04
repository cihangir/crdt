// Command readmefmt checks or formats Go code blocks in a Markdown file.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/format"
	"os"
	"regexp"
)

var goBlock = regexp.MustCompile("(?ms)^```go\\r?\\n(.*?)^```[ \\t]*\\r?$")

func main() {
	write := flag.Bool("w", false, "write formatted code blocks back to the file")
	flag.Parse()
	path := "README.md"
	if flag.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "usage: readmefmt [-w] [file.md]")
		os.Exit(2)
	}
	if flag.NArg() == 1 {
		path = flag.Arg(0)
	}
	if err := run(path, *write); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(path string, write bool) error {
	source, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var output bytes.Buffer
	position := 0
	for i, match := range goBlock.FindAllSubmatchIndex(source, -1) {
		start, end := match[2], match[3]
		formatted, err := format.Source(source[start:end])
		if err != nil {
			return fmt.Errorf("%s: Go block %d: %w", path, i+1, err)
		}
		output.Write(source[position:start])
		output.Write(formatted)
		position = end
	}
	output.Write(source[position:])
	if bytes.Equal(source, output.Bytes()) {
		return nil
	}
	if !write {
		return fmt.Errorf("%s: Go snippets need formatting; run go run ./internal/cmd/readmefmt -w", path)
	}
	return os.WriteFile(path, output.Bytes(), 0o644)
}
