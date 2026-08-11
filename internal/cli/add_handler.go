package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gp-system/framework/internal/generator"
	"github.com/gp-system/framework/internal/templates"
)

var pathParamRe = regexp.MustCompile(`\{([a-zA-Z][a-zA-Z0-9]*)\}`)

var allowedMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}

// firstPathSegment returns the first literal (non-"{param}") segment of an
// HTTP path, e.g. "/notifications/{id}/read" -> "notifications", "/" -> "",
// "/{id}" -> "". Used to derive the default handler-file group when --group
// is not given.
func firstPathSegment(httpPath string) string {
	for _, seg := range strings.Split(httpPath, "/") {
		if seg == "" || strings.HasPrefix(seg, "{") {
			continue
		}
		return seg
	}
	return ""
}

func addHandlerCmd() *cobra.Command {
	var (
		method     string
		httpPath   string
		permission string
		policyName string
		group      string
	)

	cmd := &cobra.Command{
		Use:   "handler <module> <surface> <operation>",
		Short: "Add an endpoint: TypeSpec operation + handler method skeleton",
		Long: `Inserts an operation skeleton into the surface's .tsp file and adds a
handler method to the operation's category file (module_handler.go, or
module_<group>_handler.go when --group or the path implies one), creating
that file on the first operation in the category. Run 'mise run generate'
afterwards to materialize the generated request/response types the handler
refers to, then implement the TODO body.`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			module, surface, operation := args[0], args[1], args[2]
			if err := generator.ValidateName("surface", surface); err != nil {
				return err
			}
			method = strings.ToUpper(method)
			if !slices.Contains(allowedMethods, method) {
				return fmt.Errorf("unsupported method %q (one of %v)", method, allowedMethods)
			}
			if !strings.HasPrefix(httpPath, "/") {
				return fmt.Errorf("--path must start with / (got %q)", httpPath)
			}
			// The path is rendered unescaped into @route("...") in the .tsp;
			// reject characters that would break out of the string literal or
			// inject additional TypeSpec.
			if strings.ContainsAny(httpPath, "\"\\\n\r") {
				return fmt.Errorf(`--path must not contain quotes, backslashes or newlines (got %q)`, httpPath)
			}
			if generator.ToPascal(operation) == "" {
				return fmt.Errorf("operation name %q is empty after normalization", operation)
			}
			if err := generator.ValidateIdentifier("operation", generator.ToCamel(operation)); err != nil {
				return err
			}
			permission = strings.TrimSpace(permission)
			policyName = strings.TrimSpace(policyName)
			for flag, v := range map[string]string{"--permission": permission, "--policy": policyName} {
				if strings.ContainsAny(v, "\"\\\n\r") {
					return fmt.Errorf(`%s must not contain quotes, backslashes or newlines (got %q)`, flag, v)
				}
			}

			group = strings.TrimSpace(group)
			if group == "" {
				group = firstPathSegment(httpPath)
			}
			if group != "" {
				if err := generator.ValidateSnakeName("group", generator.ToSnake(group)); err != nil {
					return err
				}
			}

			proj, err := generator.OpenProject(".")
			if err != nil {
				return err
			}
			warnProject(cmd, proj)

			entry, exists := proj.Manifest.Modules[module]
			if !exists || !slices.Contains(entry.Surfaces, surface) {
				return fmt.Errorf("module %q has no %s surface (run `add surface %s %s` first)", module, surface, module, surface)
			}

			data := generator.Data{
				Project:    projName(proj),
				GoModule:   proj.Manifest.Module,
				Module:     module,
				Surface:    surface,
				Operation:  operation,
				Method:     method,
				HTTPPath:   httpPath,
				Group:      group,
				Permission: permission,
				Policy:     policyName,
				Kit:        kitRef(""),
				Errs:       errsRef(""),
			}
			for _, m := range pathParamRe.FindAllStringSubmatch(httpPath, -1) {
				data.PathParams = append(data.PathParams, m[1])
			}

			eng := newEngine(cmd)

			// Handlers group into one file per category (the module's base
			// file when Group is empty): the first operation in a category
			// creates the file from the template, later ones are inserted as
			// a new method at the file's gpsystem:handlers anchor.
			handlerFile := filepath.Join(proj.Root, "internal", "modules", module, "surfaces", surface, "http", data.HandlerBase()+".go")
			if _, err := os.Stat(handlerFile); err == nil {
				if err := insertSnippet(eng, handlerFile, "gpsystem:handlers", "handler-method", data); err != nil {
					return err
				}
				if err := eng.FormatGoFile(handlerFile); err != nil {
					return err
				}
			} else if errors.Is(err, os.ErrNotExist) {
				files, err := eng.RenderTree(templates.FS, "handler", data, proj.Root)
				if err != nil {
					return err
				}
				if err := eng.Apply(files); err != nil {
					return err
				}
			} else {
				return err
			}

			surfaceTsp := filepath.Join(proj.Root, "spec", "typespec", "modules", module, surface+".tsp")
			if err := insertSnippet(eng, surfaceTsp, "gpsystem:operations", "tsp-operation", data); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), `
Operation %q added (%s %s). Next steps:

  mise run generate    # materialize gen.%sRequest / gen.%sResponse
  # then implement the TODO in internal/modules/%s/surfaces/%s/http/%s.go
`, operation, method, httpPath, generator.ToPascal(operation), generator.ToPascal(operation), module, surface, data.HandlerBase())
			if policyName != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "  # register policy %q in internal/modules/%s/surfaces/%s/policy/policy.go\n", policyName, module, surface)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&method, "method", "GET", "HTTP method")
	cmd.Flags().StringVar(&httpPath, "path", "/", `route path relative to the surface base, e.g. "/items/{id}"`)
	cmd.Flags().StringVar(&permission, "permission", "", `RBAC permission required by the operation (emits @permission)`)
	cmd.Flags().StringVar(&policyName, "policy", "", `policy name enforced per user for the operation (emits @policy)`)
	cmd.Flags().StringVar(&group, "group", "", `handler-file category, e.g. "notifications" (default: the --path's first literal segment; empty groups into the module's base file)`)
	return cmd
}
