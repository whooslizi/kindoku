package abi

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/andybalholm/cascadia"
	"github.com/tetratelabs/wazero/api"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// html result codes (aidoku-rs HtmlError).
const (
	htmlInvalidDescriptor = -1
	htmlInvalidString     = -2
	htmlInvalidHTML       = -3
	htmlInvalidQuery      = -4
	htmlNoResult          = -5
	htmlParseError        = -6
)

const (
	kindUnknown = iota
	kindNode
	kindTextNode
	kindDataNode
	kindComment
	kindElement
	kindElementList
	kindDocument
)

// Node is any html descriptor: document, element, text, data (script/style text) or comment.
type Node struct {
	N    *html.Node
	Base *string // shared by every node of one document
}

type Elements struct {
	List []*html.Node
	Base *string
}

// Nodes is a plain node list (child_nodes, sibling nodes of a non-element).
type Nodes struct {
	List []*html.Node
	Base *string
}

func parseDocument(s, base string, fragment bool) (*Node, error) {
	var root *html.Node
	var err error
	if fragment { // jsoup parseBodyFragment: everything goes into <body>
		root, _ = html.Parse(strings.NewReader(""))
		body := findTag(root, atom.Body)
		var nodes []*html.Node
		nodes, err = html.ParseFragment(strings.NewReader(s), &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body})
		for _, n := range nodes {
			body.AppendChild(n)
		}
	} else {
		root, err = html.Parse(strings.NewReader(s))
	}
	if err != nil {
		return nil, err
	}
	if b := findTag(root, atom.Base); b != nil { // <base href> overrides the request URL
		if href := attr(b, "href"); href != "" {
			base = resolve(base, href)
		}
	}
	return &Node{N: root, Base: &base}, nil
}

func findTag(n *html.Node, a atom.Atom) *html.Node {
	if n.Type == html.ElementNode && n.DataAtom == a {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if f := findTag(c, a); f != nil {
			return f
		}
	}
	return nil
}

func resolve(base, ref string) string {
	r, err := url.Parse(strings.TrimSpace(ref))
	if err != nil {
		return ""
	}
	if r.IsAbs() {
		return r.String()
	}
	b, err := url.Parse(base)
	if err != nil || !b.IsAbs() {
		return ""
	}
	return b.ResolveReference(r).String()
}

func attr(n *html.Node, key string) string {
	v, _ := lookupAttr(n, key)
	return v
}

func lookupAttr(n *html.Node, key string) (string, bool) {
	key = strings.ToLower(key)
	for _, a := range n.Attr {
		if a.Namespace == "" && strings.ToLower(a.Key) == key {
			return a.Val, true
		}
	}
	return "", false
}

// nodeAttr implements jsoup attr(), including the "abs:" prefix.
func nodeAttr(n *html.Node, base, key string) (string, bool) {
	if k, ok := strings.CutPrefix(strings.ToLower(key), "abs:"); ok {
		v, has := lookupAttr(n, k)
		if !has {
			return "", false
		}
		abs := resolve(base, v)
		return abs, abs != ""
	}
	return lookupAttr(n, key)
}

func setAttr(n *html.Node, key, val string) {
	removeAttr(n, key)
	n.Attr = append(n.Attr, html.Attribute{Key: strings.ToLower(key), Val: val})
}

func removeAttr(n *html.Node, key string) {
	key = strings.ToLower(key)
	out := n.Attr[:0]
	for _, a := range n.Attr {
		if strings.ToLower(a.Key) != key {
			out = append(out, a)
		}
	}
	n.Attr = out
}

func isElement(n *html.Node) bool {
	return n.Type == html.ElementNode || n.Type == html.DocumentNode
}

// isData reports a text node holding script/style content (a jsoup DataNode).
func isData(n *html.Node) bool {
	if n.Type != html.TextNode || n.Parent == nil {
		return false
	}
	switch n.Parent.DataAtom {
	case atom.Script, atom.Style:
		return true
	}
	return false
}

func nodeKind(n *html.Node) int32 {
	switch {
	case n.Type == html.DocumentNode:
		return kindDocument
	case n.Type == html.ElementNode:
		return kindElement
	case n.Type == html.CommentNode:
		return kindComment
	case isData(n):
		return kindDataNode
	case n.Type == html.TextNode:
		return kindTextNode
	}
	return kindNode
}

var blockTags = map[string]bool{}

func init() {
	for _, t := range strings.Fields(`html head body frameset script noscript style meta link title frame
		noframes section nav aside hgroup header footer p h1 h2 h3 h4 h5 h6 ul ol pre div blockquote hr
		address figure figcaption form fieldset ins del dl dt dd li table caption thead tfoot tbody
		colgroup col tr th td video audio canvas details menu plaintext template article main svg math
		center dir applet marquee listing`) {
		blockTags[t] = true
	}
}

func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\f' || r == '\r' || r == 0xa0
}

func endsWithSpace(b *strings.Builder) bool {
	s := b.String()
	return s == "" || isSpace(rune(s[len(s)-1])) || strings.HasSuffix(s, " ")
}

// appendNormalized appends s with whitespace runs collapsed to one space (jsoup semantics).
func appendNormalized(b *strings.Builder, s string) {
	prevSpace := endsWithSpace(b)
	for _, r := range s {
		if isSpace(r) {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
}

func inPre(n *html.Node) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.DataAtom == atom.Pre || p.DataAtom == atom.Textarea {
			return true
		}
	}
	return false
}

// textOf implements jsoup Element.text(); normalize=false keeps whitespace as-is (untrimmed_text).
func textOf(n *html.Node, normalize bool) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		switch {
		case n.Type == html.TextNode && !isData(n):
			if normalize && !inPre(n) {
				appendNormalized(&b, n.Data)
			} else {
				b.WriteString(n.Data)
			}
		case n.Type == html.ElementNode:
			if b.Len() > 0 && (blockTags[n.Data] || n.DataAtom == atom.Br) && !endsWithSpace(&b) {
				b.WriteByte(' ')
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
			if blockTags[n.Data] && n.NextSibling != nil && n.NextSibling.Type == html.TextNode && !endsWithSpace(&b) {
				b.WriteByte(' ')
			}
		case n.Type == html.DocumentNode:
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
	}
	walk(n)
	if normalize {
		return strings.TrimFunc(b.String(), isSpace)
	}
	return b.String()
}

func ownText(n *html.Node) string {
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch {
		case c.Type == html.TextNode && !isData(c):
			appendNormalized(&b, c.Data)
		case c.DataAtom == atom.Br && !endsWithSpace(&b):
			b.WriteByte(' ')
		}
	}
	return strings.TrimFunc(b.String(), isSpace)
}

// dataOf implements jsoup Element.data(): script/style contents and comments, recursively.
func dataOf(n *html.Node) string {
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch {
		case isData(c), c.Type == html.CommentNode:
			b.WriteString(c.Data)
		case c.Type == html.ElementNode:
			b.WriteString(dataOf(c))
		}
	}
	return b.String()
}

func outerHTML(n *html.Node) string {
	var b strings.Builder
	if n.Type == html.DocumentNode {
		return innerHTML(n)
	}
	html.Render(&b, n)
	return b.String()
}

func innerHTML(n *html.Node) string {
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		html.Render(&b, c)
	}
	return strings.TrimSpace(b.String())
}

func parseInto(ctxNode *html.Node, s string) ([]*html.Node, error) {
	if ctxNode.Type == html.DocumentNode {
		ctxNode = &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	}
	return html.ParseFragment(strings.NewReader(s), ctxNode)
}

func removeChildren(n *html.Node) {
	for n.FirstChild != nil {
		n.RemoveChild(n.FirstChild)
	}
}

func classes(n *html.Node) []string { return strings.Fields(attr(n, "class")) }

func hasClass(n *html.Node, c string) bool {
	for _, x := range classes(n) {
		if strings.EqualFold(x, c) {
			return true
		}
	}
	return false
}

func elementChildren(n *html.Node) []*html.Node {
	var out []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			out = append(out, c)
		}
	}
	return out
}

// jsoup-only index pseudo-classes, rewritten to CSS nth-child equivalents.
var indexPseudo = regexp.MustCompile(`:(eq|lt|gt)\(\s*(\d+)\s*\)`)

// jsoup accepts any unquoted attribute value ([href^=/browse?x]); CSS needs quotes for those.
var unquotedAttr = regexp.MustCompile(`\[\s*([^\s~|^$*!=\]]+)\s*([~|^$*!]?=)\s*([^"'\]\s][^\]]*?)\s*\]`)

func compileSelector(q string) (cascadia.Selector, error) {
	q = unquotedAttr.ReplaceAllStringFunc(q, func(m string) string {
		p := unquotedAttr.FindStringSubmatch(m)
		return "[" + p[1] + p[2] + strconv.Quote(p[3]) + "]"
	})
	q = indexPseudo.ReplaceAllStringFunc(q, func(m string) string {
		p := indexPseudo.FindStringSubmatch(m)
		n, _ := strconv.Atoi(p[2])
		switch p[1] {
		case "eq":
			return fmt.Sprintf(":nth-child(%d)", n+1)
		case "lt":
			return fmt.Sprintf(":nth-child(-n+%d)", n)
		default:
			return fmt.Sprintf(":nth-child(n+%d)", n+2)
		}
	})
	return cascadia.Compile(q)
}

// selectAll matches roots and their descendants (jsoup includes the root), deduplicated, in order.
func selectAll(sel cascadia.Selector, roots []*html.Node) []*html.Node {
	if len(roots) == 1 {
		return sel.MatchAll(roots[0])
	}
	seen := map[*html.Node]bool{}
	var out []*html.Node
	for _, r := range roots {
		for _, n := range sel.MatchAll(r) {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	return out
}

func (h *Host) html() map[string]any {
	str := func(m api.Module, p, n int32) (string, bool) {
		if n <= 0 {
			return "", false
		}
		return readStr(m, p, n)
	}
	node := func(rid int32) *Node { n, _ := h.Get(rid).(*Node); return n }
	elem := func(rid int32) *Node {
		if n := node(rid); n != nil && isElement(n.N) {
			return n
		}
		return nil
	}
	put := func(n *html.Node, base *string) int32 {
		if n == nil {
			return htmlNoResult
		}
		return h.Put(&Node{N: n, Base: base})
	}
	parse := func(fragment bool) func(context.Context, api.Module, int32, int32, int32, int32) int32 {
		return func(_ context.Context, m api.Module, p, n, bp, bn int32) int32 {
			s, ok := str(m, p, n)
			if !ok {
				return htmlInvalidString
			}
			base, _ := str(m, bp, bn)
			doc, err := parseDocument(s, base, fragment)
			if err != nil {
				return htmlInvalidHTML
			}
			return h.Put(doc)
		}
	}
	// elemString wraps Element functions taking a string argument and returning a status.
	elemString := func(f func(n *html.Node, s string) error) func(context.Context, api.Module, int32, int32, int32) int32 {
		return func(_ context.Context, m api.Module, rid, p, n int32) int32 {
			e := elem(rid)
			if e == nil {
				return htmlInvalidDescriptor
			}
			s, ok := readStr(m, p, n)
			if !ok {
				return htmlInvalidString
			}
			if err := f(e.N, s); err != nil {
				return htmlParseError
			}
			return 0
		}
	}
	elemValue := func(f func(n *html.Node) string) func(int32) int32 {
		return func(rid int32) int32 {
			e := elem(rid)
			if e == nil {
				return htmlInvalidDescriptor
			}
			return h.Put(f(e.N))
		}
	}
	selectFn := func(_ context.Context, m api.Module, rid, p, n int32) int32 {
		q, ok := str(m, p, n)
		if !ok {
			return htmlInvalidString
		}
		var roots []*html.Node
		var base *string
		switch v := h.Get(rid).(type) {
		case *Elements:
			roots, base = v.List, v.Base
		case *Node:
			if !isElement(v.N) {
				return htmlNoResult
			}
			roots, base = []*html.Node{v.N}, v.Base
		case nil:
			return htmlInvalidDescriptor
		default:
			return htmlNoResult
		}
		sel, err := compileSelector(q)
		if err != nil {
			h.Log("html: invalid selector " + strconv.Quote(q) + ": " + err.Error())
			return htmlInvalidQuery
		}
		return h.Put(&Elements{List: selectAll(sel, roots), Base: base})
	}

	return map[string]any{
		"parse":          parse(false),
		"parse_fragment": parse(true),
		"escape": func(_ context.Context, m api.Module, p, n int32) int32 {
			s, ok := str(m, p, n)
			if !ok {
				return htmlInvalidString
			}
			return h.Put(html.EscapeString(s))
		},
		"unescape": func(_ context.Context, m api.Module, p, n int32) int32 {
			s, ok := str(m, p, n)
			if !ok {
				return htmlInvalidString
			}
			return h.Put(html.UnescapeString(s))
		},
		"kind": func(rid int32) int32 {
			switch v := h.Get(rid).(type) {
			case *Node:
				return nodeKind(v.N)
			case *Elements:
				return kindElementList
			case nil:
				return htmlInvalidDescriptor
			}
			return kindUnknown
		},

		"child_nodes": func(rid int32) int32 {
			n := node(rid)
			if n == nil {
				return htmlInvalidDescriptor
			}
			var list []*html.Node
			for c := n.N.FirstChild; c != nil; c = c.NextSibling {
				list = append(list, c)
			}
			return h.Put(&Nodes{List: list, Base: n.Base})
		},
		"has_attr": func(_ context.Context, m api.Module, rid, p, n int32) int32 {
			e := node(rid)
			if e == nil {
				return htmlInvalidDescriptor
			}
			k, ok := str(m, p, n)
			if !ok {
				return htmlInvalidString
			}
			if _, has := nodeAttr(e.N, *e.Base, k); has {
				return 1
			}
			return 0
		},
		"set_attr": func(_ context.Context, m api.Module, rid, kp, kn, vp, vn int32) int32 {
			e := node(rid)
			if e == nil {
				return htmlInvalidDescriptor
			}
			k, ok := str(m, kp, kn)
			if !ok {
				return htmlInvalidString
			}
			v, ok := readStr(m, vp, vn)
			if !ok {
				return htmlInvalidString
			}
			if v == "" {
				removeAttr(e.N, k)
			} else {
				setAttr(e.N, k, v)
			}
			return 0
		},
		"remove_attr": func(_ context.Context, m api.Module, rid, p, n int32) int32 {
			e := node(rid)
			if e == nil {
				return htmlInvalidDescriptor
			}
			k, ok := str(m, p, n)
			if !ok {
				return htmlInvalidString
			}
			removeAttr(e.N, k)
			return 0
		},

		"set_text": elemString(func(n *html.Node, s string) error {
			removeChildren(n)
			n.AppendChild(&html.Node{Type: html.TextNode, Data: s})
			return nil
		}),
		"set_html": elemString(func(n *html.Node, s string) error {
			nodes, err := parseInto(n, s)
			if err == nil {
				removeChildren(n)
				for _, c := range nodes {
					n.AppendChild(c)
				}
			}
			return err
		}),
		"prepend": elemString(func(n *html.Node, s string) error {
			nodes, err := parseInto(n, s)
			first := n.FirstChild
			for _, c := range nodes {
				n.InsertBefore(c, first)
			}
			return err
		}),
		"append": elemString(func(n *html.Node, s string) error {
			nodes, err := parseInto(n, s)
			for _, c := range nodes {
				n.AppendChild(c)
			}
			return err
		}),
		"children": func(rid int32) int32 {
			e := elem(rid)
			if e == nil {
				return htmlInvalidDescriptor
			}
			return h.Put(&Elements{List: elementChildren(e.N), Base: e.Base})
		},
		"base_uri": func(rid int32) int32 {
			e := elem(rid)
			if e == nil {
				return htmlInvalidDescriptor
			}
			return h.Put(*e.Base)
		},
		"own_text": elemValue(ownText),
		"data": func(rid int32) int32 {
			n := node(rid)
			switch {
			case n == nil:
				return htmlInvalidDescriptor
			case isElement(n.N):
				return h.Put(dataOf(n.N))
			case isData(n.N), n.N.Type == html.CommentNode:
				return h.Put(n.N.Data)
			}
			return htmlInvalidDescriptor
		},
		"id":         elemValue(func(n *html.Node) string { return attr(n, "id") }),
		"tag_name":   elemValue(func(n *html.Node) string { return n.Data }),
		"class_name": elemValue(func(n *html.Node) string { return strings.TrimSpace(attr(n, "class")) }),
		"has_class": func(_ context.Context, m api.Module, rid, p, n int32) int32 {
			e := elem(rid)
			if e == nil {
				return htmlInvalidDescriptor
			}
			c, ok := str(m, p, n)
			if !ok {
				return htmlInvalidString
			}
			if hasClass(e.N, c) {
				return 1
			}
			return 0
		},
		"add_class": elemString(func(n *html.Node, c string) error {
			if !hasClass(n, c) {
				setAttr(n, "class", strings.Join(append(classes(n), c), " "))
			}
			return nil
		}),
		"remove_class": elemString(func(n *html.Node, c string) error {
			var keep []string
			for _, x := range classes(n) {
				if !strings.EqualFold(x, c) {
					keep = append(keep, x)
				}
			}
			setAttr(n, "class", strings.Join(keep, " "))
			return nil
		}),

		"first": func(rid int32) int32 {
			l, ok := h.Get(rid).(*Elements)
			if !ok {
				return htmlInvalidDescriptor
			}
			if len(l.List) == 0 {
				return htmlNoResult
			}
			return put(l.List[0], l.Base)
		},
		"last": func(rid int32) int32 {
			l, ok := h.Get(rid).(*Elements)
			if !ok {
				return htmlInvalidDescriptor
			}
			if len(l.List) == 0 {
				return htmlNoResult
			}
			return put(l.List[len(l.List)-1], l.Base)
		},
		"get": func(rid, i int32) int32 {
			var list []*html.Node
			var base *string
			switch v := h.Get(rid).(type) {
			case *Elements:
				list, base = v.List, v.Base
			case *Nodes:
				list, base = v.List, v.Base
			default:
				return htmlInvalidDescriptor
			}
			if i < 0 || int(i) >= len(list) {
				return htmlNoResult
			}
			return put(list[i], base)
		},
		"size": func(rid int32) int32 {
			switch v := h.Get(rid).(type) {
			case *Elements:
				return int32(len(v.List))
			case *Nodes:
				return int32(len(v.List))
			}
			return htmlInvalidDescriptor
		},

		"parent": func(rid int32) int32 {
			n := node(rid)
			if n == nil {
				return htmlInvalidDescriptor
			}
			return put(n.N.Parent, n.Base)
		},
		"siblings": func(rid int32) int32 {
			n := node(rid)
			if n == nil {
				return htmlInvalidDescriptor
			}
			var list []*html.Node
			if p := n.N.Parent; p != nil {
				for c := p.FirstChild; c != nil; c = c.NextSibling {
					if c != n.N && (c.Type == html.ElementNode || n.N.Type != html.ElementNode) {
						list = append(list, c)
					}
				}
			}
			if n.N.Type == html.ElementNode {
				return h.Put(&Elements{List: list, Base: n.Base})
			}
			return h.Put(&Nodes{List: list, Base: n.Base})
		},
		"next": func(rid int32) int32 {
			n := node(rid)
			if n == nil {
				return htmlInvalidDescriptor
			}
			s := n.N.NextSibling
			for n.N.Type == html.ElementNode && s != nil && s.Type != html.ElementNode {
				s = s.NextSibling
			}
			return put(s, n.Base)
		},
		"previous": func(rid int32) int32 {
			n := node(rid)
			if n == nil {
				return htmlInvalidDescriptor
			}
			s := n.N.PrevSibling
			for n.N.Type == html.ElementNode && s != nil && s.Type != html.ElementNode {
				s = s.PrevSibling
			}
			return put(s, n.Base)
		},

		"attr": func(_ context.Context, m api.Module, rid, p, n int32) int32 {
			k, ok := str(m, p, n)
			if !ok {
				return htmlInvalidString
			}
			switch v := h.Get(rid).(type) {
			case *Elements:
				for _, e := range v.List {
					if val, has := nodeAttr(e, *v.Base, k); has {
						return h.Put(val)
					}
				}
				return h.Put("")
			case *Node:
				val, _ := nodeAttr(v.N, *v.Base, k)
				return h.Put(val)
			case nil:
				return htmlInvalidDescriptor
			}
			return htmlNoResult
		},
		"outer_html": func(rid int32) int32 {
			switch v := h.Get(rid).(type) {
			case *Elements:
				parts := make([]string, len(v.List))
				for i, e := range v.List {
					parts[i] = outerHTML(e)
				}
				return h.Put(strings.Join(parts, "\n"))
			case *Node:
				return h.Put(outerHTML(v.N))
			case nil:
				return htmlInvalidDescriptor
			}
			return htmlNoResult
		},
		"remove": func(rid int32) int32 {
			var list []*html.Node
			switch v := h.Get(rid).(type) {
			case *Elements:
				list = v.List
			case *Node:
				list = []*html.Node{v.N}
			case nil:
				return htmlInvalidDescriptor
			default:
				return htmlNoResult
			}
			for _, n := range list {
				if n.Parent != nil {
					n.Parent.RemoveChild(n)
				}
			}
			return 0
		},

		"select": selectFn,
		"select_first": func(ctx context.Context, m api.Module, rid, p, n int32) int32 {
			r := selectFn(ctx, m, rid, p, n)
			if r < 0 {
				return r
			}
			l := h.Get(r).(*Elements)
			h.Remove(r)
			if len(l.List) == 0 {
				return htmlNoResult
			}
			return put(l.List[0], l.Base)
		},
		"text": func(rid int32) int32 {
			switch v := h.Get(rid).(type) {
			case *Elements:
				parts := make([]string, 0, len(v.List))
				for _, e := range v.List {
					if t := textOf(e, true); t != "" {
						parts = append(parts, t)
					}
				}
				return h.Put(strings.Join(parts, " "))
			case *Node:
				if isElement(v.N) {
					return h.Put(textOf(v.N, true))
				}
				if v.N.Type == html.TextNode && !isData(v.N) {
					var b strings.Builder
					appendNormalized(&b, v.N.Data)
					return h.Put(b.String())
				}
				return htmlNoResult
			case nil:
				return htmlInvalidDescriptor
			}
			return htmlNoResult
		},
		"untrimmed_text": func(rid int32) int32 {
			switch v := h.Get(rid).(type) {
			case *Elements:
				parts := make([]string, len(v.List))
				for i, e := range v.List {
					parts[i] = textOf(e, false)
				}
				return h.Put(strings.Join(parts, " "))
			case *Node:
				if isElement(v.N) {
					return h.Put(textOf(v.N, false))
				}
				if v.N.Type == html.TextNode {
					return h.Put(v.N.Data)
				}
				return htmlNoResult
			case nil:
				return htmlInvalidDescriptor
			}
			return htmlNoResult
		},
		"html": func(rid int32) int32 {
			switch v := h.Get(rid).(type) {
			case *Elements:
				parts := make([]string, len(v.List))
				for i, e := range v.List {
					parts[i] = innerHTML(e)
				}
				return h.Put(strings.Join(parts, "\n"))
			case *Node:
				if isElement(v.N) {
					return h.Put(innerHTML(v.N))
				}
				return htmlNoResult
			case nil:
				return htmlInvalidDescriptor
			}
			return htmlNoResult
		},
	}
}
