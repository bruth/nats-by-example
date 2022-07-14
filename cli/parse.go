package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	Shell      = "shell"
	CLI        = "cli"
	Go         = "go"
	Rust       = "rust"
	Java       = "java"
	CSharp     = "csharp"
	TypeScript = "typescript"
	C          = "c"
	Python     = "python"
	Ruby       = "ruby"
	Elixir     = "elixir"
)

var (
	// Available client SDKs..
	availableLanguages = map[string]struct{}{
		Shell:      {},
		CLI:        {},
		Go:         {},
		Rust:       {},
		Java:       {},
		CSharp:     {},
		TypeScript: {},
		C:          {},
		Python:     {},
		Ruby:       {},
		Elixir:     {},
	}

	// TODO: add more as they become supported..
	languageMains = map[string]string{
		Go:     "main.go",
		Python: "main.py",
		CLI:    "main.sh",
		Shell:  "main.sh",
	}
)

type Root struct {
	Path       string
	Categories []*Category
}

type Category struct {
	Name        string
	Path        string
	Title       string
	Description string
	Examples    []*Example
}

type Example struct {
	Name            string
	Path            string
	Title           string
	Description     string
	Implementations []*Implementation
}

type Implementation struct {
	Name     string
	Path     string
	Language string
	MainFile string
	Blocks   []*Block
}

type BlockType uint8

const (
	EmptyBlock BlockType = iota
	CodeBlock
	SingleLineCommentBlock
	MultiLineCommentBlock
)

type Block struct {
	Type      BlockType
	Lines     []string
	StartLine int
	EndLine   int
}

type LineType uint8

const (
	EmptyLine LineType = iota
	NormalLine
	SingleCommentLine
	OpenMultiCommentLine
	CloseMultiCommentLine
)

var (
	shebangLineRe                 = regexp.MustCompile(`^#!`)
	hashLineCommentRe             = regexp.MustCompile(`^\s*#`)
	cStyleSingleCommentLineRe     = regexp.MustCompile(`^\s*\/\/`)
	cStyleOpenMultiCommentLineRe  = regexp.MustCompile(`^\s*\/\*`)
	cStyleCloseMultiCommentLineRe = regexp.MustCompile(`\*\/`)
)

// One limitiation is that it does not currently handle trailing multi-line
// comments, such as:
//	func() int {/*
//    a := 1
//  */
//    b := 2
// Since this code is scoped to well written examples, it should not be an issue
// in practice.
func parseLineType(lang, line string) LineType {
	if strings.TrimSpace(line) == "" {
		return EmptyLine
	}

	switch lang {
	case Shell:
		if shebangLineRe.MatchString(line) {
			return NormalLine
		}
		if hashLineCommentRe.MatchString(line) {
			return SingleCommentLine
		}
		return NormalLine

	case Go, CSharp, Java, TypeScript, Rust, C:
		if cStyleSingleCommentLineRe.MatchString(line) {
			return SingleCommentLine
		}
		if cStyleOpenMultiCommentLineRe.MatchString(line) {
			// Inline multi-line comment, e.g. func foo(a int/*, b int*/)
			if cStyleCloseMultiCommentLineRe.MatchString(line) {
				return NormalLine
			}
			return OpenMultiCommentLine
		}
		if cStyleCloseMultiCommentLineRe.MatchString(line) {
			return CloseMultiCommentLine
		}
		return NormalLine

	case Python, Ruby, Elixir:
		if hashLineCommentRe.MatchString(line) {
			return SingleCommentLine
		}
		return NormalLine
	}

	panic(fmt.Sprintf("%s not currently supported", lang))
}

func parseReader(lang string, r io.Reader) ([]*Block, error) {
	var (
		lineNum      int
		block        = &Block{StartLine: 1, EndLine: 1}
		blocks       = []*Block{block}
		endMultiLine = false
	)

	// Read each line, keeping track of comment and code lines.
	sc := bufio.NewScanner(r)
	for sc.Scan() {

		lineNum++
		line := sc.Text()

		if endMultiLine {
			block = &Block{
				StartLine: lineNum,
				EndLine:   lineNum,
			}
			blocks = append(blocks, block)
		}

		endMultiLine = false

		lineType := parseLineType(lang, line)

		switch lineType {
		// Does not differentiate a boundary.. simply append to current block.
		case EmptyLine:
			block.Lines = append(block.Lines, line)

		case NormalLine:
			switch block.Type {
			// If not already a comment block, a normal line implies code.
			case EmptyBlock:
				block.Type = CodeBlock

			// Boundary from single line comment -> code
			case SingleLineCommentBlock:
				block = &Block{
					Type:      CodeBlock,
					StartLine: lineNum,
					EndLine:   lineNum,
				}
				blocks = append(blocks, block)

			case CodeBlock:
			case MultiLineCommentBlock:
			}

		case SingleCommentLine:
			switch block.Type {
			case EmptyBlock:
				block.Type = SingleLineCommentBlock

			// Boundary from code -> comment.
			case CodeBlock:
				block = &Block{
					Type:      SingleLineCommentBlock,
					StartLine: lineNum,
					EndLine:   lineNum,
				}
				blocks = append(blocks, block)

			// Single line comment within a multi line is just a normal line.
			case MultiLineCommentBlock:
			case SingleLineCommentBlock:
			}

		case OpenMultiCommentLine:
			switch block.Type {
			case EmptyBlock:
				block.Type = MultiLineCommentBlock

			// Boundary code -> multi-line
			case CodeBlock:
				block = &Block{
					Type:      MultiLineCommentBlock,
					StartLine: lineNum,
					EndLine:   lineNum,
				}
				blocks = append(blocks, block)

			// An opening comment or single comment in an existing multi-line
			// comment has no effect.
			case MultiLineCommentBlock:
			case SingleLineCommentBlock:
			}

		case CloseMultiCommentLine:
			switch block.Type {
			// Only valid block type where this line is relevant.
			case MultiLineCommentBlock:
				endMultiLine = true

			case EmptyBlock:
			case CodeBlock:
			case SingleLineCommentBlock:
				panic("syntax error while parsing blocks")
			}
		}

		// Finally append the line and set the end line of the block.
		block.Lines = append(block.Lines, line)
		block.EndLine = lineNum
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	return blocks, nil
}

func readImplmentationDir(path, name string) (*Implementation, error) {
	x := Implementation{
		Name: name,
		Path: path,
	}
	lang := strings.ToLower(name)

	// Default to script if not known.
	if _, ok := availableLanguages[lang]; !ok {
		lang = Shell
	}

	// Determine main file name.
	mainFile, ok := languageMains[lang]
	if !ok {
		return nil, fmt.Errorf("language %q not yet supported", lang)
	}

	// Ensure main file exists.
	f, err := os.Open(filepath.Join(path, mainFile))
	if err != nil {
		return nil, fmt.Errorf("open main file: %w", err)
	}
	defer f.Close()

	blocks, err := parseReader(lang, f)
	x.Language = lang
	x.MainFile = mainFile
	x.Blocks = blocks

	return &x, nil
}

func readExampleDir(path, name string) (*Example, error) {
	x := Example{
		Name:  name,
		Path:  path,
		Title: strings.Title(name),
	}

	// Read meta file.
	meta, err := fs.ReadFile(os.DirFS(path), "meta.yaml")
	if err == nil {
		if err := yaml.Unmarshal(meta, &x); err != nil {
			return nil, fmt.Errorf("parse yaml: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read meta: %w", err)
	}

	dirs, err := fs.ReadDir(os.DirFS(path), ".")
	if err != nil {
		return nil, fmt.Errorf("read dir: %w", err)
	}

	ims := make(map[string]*Implementation)
	for _, e := range dirs {
		if !e.IsDir() {
			continue
		}

		name := e.Name()
		path := filepath.Join(path, name)
		im, err := readImplmentationDir(path, name)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				log.Printf("%s: no main file. skipping...", path)
				continue
			}
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		ims[name] = im
	}

	// Append the reminder to the end.
	for _, i := range ims {
		x.Implementations = append(x.Implementations, i)
	}

	return &x, nil
}

func readCategoryDir(path, name string) (*Category, error) {
	c := Category{
		Name:  name,
		Path:  path,
		Title: strings.Title(name),
	}

	type categoryMeta struct {
		Title       string
		Description string
		Examples    []string
	}

	var cm categoryMeta

	// Read meta file.
	meta, err := fs.ReadFile(os.DirFS(path), "meta.yaml")
	if err == nil {
		if err := yaml.Unmarshal(meta, &cm); err != nil {
			return nil, fmt.Errorf("parse yaml: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read meta: %w", err)
	}

	if cm.Title != "" {
		c.Title = cm.Title
	}
	c.Description = cm.Description

	dirs, err := fs.ReadDir(os.DirFS(path), ".")
	if err != nil {
		return nil, fmt.Errorf("read dir: %w", err)
	}

	exs := make(map[string]*Example)
	for _, e := range dirs {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		path := filepath.Join(path, name)
		ex, err := readExampleDir(path, name)
		if err != nil {
			return nil, fmt.Errorf("read example: %s: %w", name, err)
		}

		if len(ex.Implementations) > 0 {
			exs[name] = ex
		}
	}

	// Append ordered examples first.
	for _, name := range cm.Examples {
		c.Examples = append(c.Examples, exs[name])
		delete(exs, name)
	}

	// Append the reminder to the end.
	for _, e := range exs {
		c.Examples = append(c.Examples, e)
	}

	return &c, nil
}

func parseExamples(path string) (*Root, error) {
	r := Root{
		Path: path,
	}

	type rootMeta struct {
		Categories []string
	}
	var rm rootMeta

	// Read meta file.
	meta, err := fs.ReadFile(os.DirFS(path), "meta.yaml")
	if err == nil {
		if err := yaml.Unmarshal(meta, &rm); err != nil {
			return nil, fmt.Errorf("root: parse yaml: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("root: read meta: %w", err)
	}

	// Root will read the categories.
	dirs, err := fs.ReadDir(os.DirFS(path), ".")
	if err != nil {
		return nil, fmt.Errorf("root: read dir: %w", err)
	}

	cats := make(map[string]*Category)
	for _, e := range dirs {
		if !e.IsDir() {
			continue
		}

		name := e.Name()
		path := filepath.Join(path, name)
		c, err := readCategoryDir(path, name)
		if err != nil {
			return nil, fmt.Errorf("read category: %s: %w", name, err)
		}

		if len(c.Examples) > 0 {
			cats[name] = c
		}
	}

	// Append ordered examples first.
	for _, name := range rm.Categories {
		r.Categories = append(r.Categories, cats[name])
		delete(cats, name)
	}

	// Append the reminder to the end.
	for _, c := range cats {
		r.Categories = append(r.Categories, c)
	}

	return &r, nil
}
