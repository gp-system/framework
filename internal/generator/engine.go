package generator

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"golang.org/x/tools/imports"
)

// Engine renders embedded template trees into a target directory with the
// kit's safety rules: the full file plan is computed up front, conflicts
// abort the whole run (unless Force), nothing is written until every file
// rendered successfully, and .go output is gofmt/goimports-formatted.
type Engine struct {
	DryRun bool
	Force  bool
	Out    io.Writer
}

// File is one planned output file.
type File struct {
	Path    string // target path, absolute or relative to CWD
	Content []byte
}

var funcMap = template.FuncMap{
	"pascal": ToPascal,
	"camel":  ToCamel,
	"snake":  ToSnake,
	"kebab":  ToKebab,
	"lower":  strings.ToLower,
	"upper":  strings.ToUpper,
}

// placeholders maps path segments like __module__ to data fields.
type pathData interface{ PathVars() map[string]string }

// RenderTree renders every file under root in tfs into targetRoot.
// Path placeholders (__module__, __surface__, ...) are substituted from
// data.PathVars(); the .tmpl suffix is stripped; rendered .go files are
// formatted with goimports and fail loudly on invalid output.
func (e *Engine) RenderTree(tfs fs.FS, root string, data pathData, targetRoot string) ([]File, error) {
	sub, err := fs.Sub(tfs, root)
	if err != nil {
		return nil, err
	}

	var files []File
	err = fs.WalkDir(sub, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := fs.ReadFile(sub, path)
		if err != nil {
			return err
		}

		target := strings.TrimSuffix(path, ".tmpl")
		for k, v := range data.PathVars() {
			target = strings.ReplaceAll(target, "__"+k+"__", v)
		}

		content := raw
		if strings.HasSuffix(path, ".tmpl") {
			tpl, err := template.New(path).Funcs(funcMap).Parse(string(raw))
			if err != nil {
				return fmt.Errorf("parse template %s: %w", path, err)
			}
			var buf bytes.Buffer
			if err := tpl.Execute(&buf, data); err != nil {
				return fmt.Errorf("render %s: %w", path, err)
			}
			content = buf.Bytes()

			if strings.HasSuffix(target, ".go") {
				formatted, err := imports.Process(target, content, nil)
				if err != nil {
					return fmt.Errorf("template %s rendered invalid Go:\n%s\n%w", path, content, err)
				}
				content = formatted
			}
		}

		files = append(files, File{
			Path:    filepath.Join(targetRoot, filepath.FromSlash(target)),
			Content: content,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

// CopyTree copies every file under root in tfs verbatim (no rendering, no
// suffix stripping) into targetRoot. Used for the oapi-codegen templates,
// which themselves contain template actions.
func (e *Engine) CopyTree(tfs fs.FS, root, targetRoot string) ([]File, error) {
	sub, err := fs.Sub(tfs, root)
	if err != nil {
		return nil, err
	}
	var files []File
	err = fs.WalkDir(sub, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := fs.ReadFile(sub, path)
		if err != nil {
			return err
		}
		files = append(files, File{Path: filepath.Join(targetRoot, filepath.FromSlash(path)), Content: raw})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

// Apply writes the planned files. Existing targets abort the whole run before
// anything is written, unless Force; with Force the run proceeds but every
// file that will be overwritten is listed first. DryRun only reports the plan.
func (e *Engine) Apply(files []File) error {
	var conflicts []string
	for _, f := range files {
		if _, err := os.Stat(f.Path); err == nil {
			conflicts = append(conflicts, f.Path)
		}
	}
	if len(conflicts) > 0 {
		if !e.Force {
			return fmt.Errorf("refusing to overwrite existing files (use --force):\n  %s", strings.Join(conflicts, "\n  "))
		}
		verb := "overwriting"
		if e.DryRun {
			verb = "would overwrite"
		}
		for _, c := range conflicts {
			fmt.Fprintf(e.out(), "%s %s\n", verb, c)
		}
	}

	for _, f := range files {
		if e.DryRun {
			fmt.Fprintf(e.out(), "would write %s (%d bytes)\n", f.Path, len(f.Content))
			continue
		}
		if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(f.Path, f.Content, 0o644); err != nil {
			return err
		}
		fmt.Fprintf(e.out(), "wrote %s\n", f.Path)
	}
	return nil
}

func (e *Engine) out() io.Writer {
	if e.Out != nil {
		return e.Out
	}
	return os.Stdout
}

// RenderSnippet renders a snippet template (anchor-insertion fragment) with
// the same function map the tree renderer uses.
func RenderSnippet(text string, data any) (string, error) {
	tpl, err := template.New("snippet").Funcs(funcMap).Parse(text)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// FormatGoFile runs goimports on a file in place: used after anchor
// insertions so inserted imports get grouped and sorted. No-op in dry-run.
func (e *Engine) FormatGoFile(path string) error {
	if e.DryRun {
		return nil
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	formatted, err := imports.Process(path, src, nil)
	if err != nil {
		return fmt.Errorf("format %s after insertion: %w", path, err)
	}
	if !bytes.Equal(src, formatted) {
		return os.WriteFile(path, formatted, 0o644)
	}
	return nil
}
