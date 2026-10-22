// gen_responses writes src/discord/helpers/response_catalog.json: every built-in command response, as the dashboard's
// Commands → Responses tab lists it. Run it with `go generate ./src/discord/helpers` after changing a command's embeds.
//
// It reads the Preset(...) calls (and &discordgo.MessageEmbed literals) of src/discord/helpers/command_embeds.go, which is
// split into "// <dir>/<file>.go" sections, plus those of the command files themselves. A section belongs to the command
// whose types.Command literal is in that file. Each part the command fills in (a name, a number, a built-up string) is
// written as "…"; the bot reads those parts back out of the real response (see extractParts in embed_helper.go).
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const changing = "…"

type response struct {
	Key         string `json:"key"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Look        string `json:"look"`
	Description string `json:"description,omitempty"`
	HasFields   bool   `json:"has_fields,omitempty"`
}

type command struct {
	Name      string     `json:"name"`
	Responses []response `json:"responses"`
}

var (
	section = regexp.MustCompile(`^// ([a-z]+)/([a-z_]+)\.go$`)
	verb    = regexp.MustCompile(`%[-+# 0-9.*]*[a-zA-Z]`)
	looks   = map[string]string{"success": "success", "error": "error", "caution": "caution", "loading": "loading", "info": "info", "neutral": "info", "transparent": "info"}
	colors  = map[string]string{"ColorSuccess": "success", "ColorError": "error", "ColorCaution": "caution", "ColorLoading": "loading", "ColorInfo": "info", "ColorNeutral": "info", "ColorTransparent": "info"}
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	fset := token.NewFileSet()
	byCommand := map[string][]response{}
	add := func(name string, rs []response) {
		for _, r := range rs {
			r.Key = name + "." + r.Slug
			dup := false
			for _, e := range byCommand[name] {
				dup = dup || e.Key == r.Key
			}
			if !dup {
				byCommand[name] = append(byCommand[name], r)
			}
		}
	}

	// Command files: their own Preset calls.
	files, _ := filepath.Glob(filepath.Join(root, "src/discord/commands/*/*.go"))
	names := map[string]string{} // "dir/file" -> command name
	for _, path := range files {
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		check(err)
		key := filepath.Base(filepath.Dir(path)) + "/" + strings.TrimSuffix(filepath.Base(path), ".go")
		cmds := commandNames(f)
		if len(cmds) == 0 {
			continue
		}
		names[key] = cmds[0]
		if len(cmds) > 1 {
			fmt.Fprintf(os.Stderr, "warning: %s defines %d commands; responses go to %q\n", key, len(cmds), cmds[0])
		}
		add(cmds[0], scan(f, nil))
	}

	// command_embeds.go: sections by source file.
	path := filepath.Join(root, "src/discord/helpers/command_embeds.go")
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	check(err)
	type mark struct {
		pos token.Pos
		key string
	}
	var marks []mark
	for _, g := range f.Comments {
		for _, c := range g.List {
			if m := section.FindStringSubmatch(c.Text); m != nil {
				marks = append(marks, mark{c.Pos(), m[1] + "/" + m[2]})
			}
		}
	}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		key := ""
		for _, m := range marks {
			if m.pos < fn.Pos() {
				key = m.key
			}
		}
		name, ok := names[key]
		if !ok {
			if key != "" && key != "general/player_register_verify" { // a modal, answered outside the command interaction
				fmt.Fprintf(os.Stderr, "warning: no command found for %s (%s)\n", key, fn.Name.Name)
			}
			continue
		}
		add(name, scan(fn, fn))
	}

	out := struct {
		Commands []command `json:"commands"`
	}{}
	for name, rs := range byCommand {
		if len(rs) > 0 {
			out.Commands = append(out.Commands, command{Name: name, Responses: rs})
		}
	}
	sort.Slice(out.Commands, func(a, b int) bool { return out.Commands[a].Name < out.Commands[b].Name })
	data, err := json.MarshalIndent(out, "", "\t")
	check(err)
	check(os.WriteFile(filepath.Join(root, "src/discord/helpers/response_catalog.json"), append(data, '\n'), 0o644))
	total := 0
	for _, c := range out.Commands {
		total += len(c.Responses)
	}
	fmt.Printf("wrote %d responses for %d commands\n", total, len(out.Commands))
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// commandNames are the Name of each types.Command literal in the file.
func commandNames(f *ast.File) []string {
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok || !isSel(lit.Type, "types", "Command") {
			return true
		}
		for _, el := range lit.Elts {
			if kv, ok := el.(*ast.KeyValueExpr); ok && ident(kv.Key) == "Name" {
				if s, ok := strLit(kv.Value); ok {
					out = append(out, s)
				}
			}
		}
		return true
	})
	return out
}

func ident(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

func isSel(e ast.Expr, pkg, name string) bool {
	for {
		if u, ok := e.(*ast.UnaryExpr); ok {
			e = u.X
			continue
		}
		break
	}
	s, ok := e.(*ast.SelectorExpr)
	return ok && ident(s.X) == pkg && s.Sel.Name == name
}

func strLit(e ast.Expr) (string, bool) {
	b, ok := e.(*ast.BasicLit)
	if !ok || b.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(b.Value)
	return s, err == nil
}

// scan finds the responses built under root; fn (may be nil) is the function whose assignments resolve variables.
func scan(root ast.Node, fn *ast.FuncDecl) []response {
	var out []response
	ast.Inspect(root, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			if ident(x.Fun) != "Preset" || len(x.Args) != 2 {
				return true
			}
			opts, ok := x.Args[1].(*ast.CompositeLit)
			if !ok {
				return true
			}
			look := "default"
			if s, ok := resolve(x.Args[0], fn); ok {
				look = looks[s]
				if look == "" {
					look = "default"
				}
			}
			if r, ok := build(opts, look, fn); ok {
				out = append(out, r)
			}
		case *ast.CompositeLit:
			if !isSel(x.Type, "discordgo", "MessageEmbed") {
				return true
			}
			look := "default"
			for _, el := range x.Elts {
				if kv, ok := el.(*ast.KeyValueExpr); ok && ident(kv.Key) == "Color" && ident(kv.Value) != "" {
					if l := colors[ident(kv.Value)]; l != "" {
						look = l
					}
				}
			}
			if r, ok := build(x, look, fn); ok {
				out = append(out, r)
			}
		}
		return true
	})
	return out
}

func build(lit *ast.CompositeLit, look string, fn *ast.FuncDecl) (response, bool) {
	var title, desc ast.Expr
	r := response{Look: look}
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		switch ident(kv.Key) {
		case "Title":
			title = kv.Value
		case "Description":
			desc = kv.Value
		case "Fields":
			r.HasFields = true
		}
	}
	if title == nil {
		return r, false
	}
	r.Title = render(title, fn)
	if strings.Trim(r.Title, changing+" ") == "" {
		return r, false // a title that is wholly dynamic has no name to restyle by
	}
	if desc != nil {
		if d := render(desc, fn); strings.Trim(d, changing+" \n") != "" {
			r.Description = d
		}
	}
	r.Slug = slug(r.Title)
	return r, true
}

// resolve is the string an expression holds when it is a literal, or a variable assigned exactly one literal.
func resolve(e ast.Expr, fn *ast.FuncDecl) (string, bool) {
	if s, ok := strLit(e); ok {
		return s, true
	}
	name := ident(e)
	if name == "" || fn == nil {
		return "", false
	}
	var found []string
	ast.Inspect(fn, func(n ast.Node) bool {
		if a, ok := n.(*ast.AssignStmt); ok && len(a.Lhs) == len(a.Rhs) {
			for i, l := range a.Lhs {
				if ident(l) == name {
					if s, ok := strLit(a.Rhs[i]); ok {
						found = append(found, s)
					} else {
						found = append(found, "\x00")
					}
				}
			}
		}
		return true
	})
	if len(found) == 1 && found[0] != "\x00" {
		return found[0], true
	}
	return "", false
}

// render is what an expression says, with each dynamic part as "…".
func render(e ast.Expr, fn *ast.FuncDecl) string {
	if s, ok := resolve(e, fn); ok {
		return s
	}
	switch x := e.(type) {
	case *ast.BinaryExpr:
		if x.Op == token.ADD {
			return collapse(render(x.X, fn) + render(x.Y, fn))
		}
	case *ast.CallExpr:
		if sel, ok := x.Fun.(*ast.SelectorExpr); ok && ident(sel.X) == "fmt" && sel.Sel.Name == "Sprintf" && len(x.Args) > 0 {
			if format, ok := resolve(x.Args[0], fn); ok {
				format = strings.ReplaceAll(format, "%%", "\x01")
				return collapse(strings.ReplaceAll(verb.ReplaceAllString(format, changing), "\x01", "%"))
			}
		}
	case *ast.ParenExpr:
		return render(x.X, fn)
	}
	return changing
}

func collapse(s string) string { return strings.ReplaceAll(s, changing+changing, changing) }

// slug keeps in step with helpers.ResponseTitleSlug (a test there compares the two).
func slug(title string) string {
	for _, sep := range []string{" — ", " · "} {
		if cut := strings.Index(title, sep); cut >= 0 {
			title = title[:cut]
		}
	}
	var b strings.Builder
	dash := true
	for _, r := range strings.ToLower(title) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	if s := strings.Trim(b.String(), "-"); s != "" {
		return s
	}
	return "response"
}
