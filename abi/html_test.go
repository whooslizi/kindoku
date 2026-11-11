package abi

import (
	"testing"
)

const testPage = `<html><head><base href="/sub/"><script>var x = 1;</script></head><body>
<div id="main" class="box  wide">
  <h1>Title <b>bold</b></h1>
  <p>first   line<br>second line</p>
  <ul><li class="ch">One</li><li class="ch">Two</li><li class="ch">Three</li></ul>
  <a href="page/2">next</a>
  <!-- note -->
  <pre>  keep   spaces </pre>
</div></body></html>`

func TestHTML(t *testing.T) {
	h, g := newGuest(t)
	m := h.html()
	parse := fn[fnMemParse](t, m, "parse")
	sel := fn[fnMemRid](t, m, "select")
	selFirst := fn[fnMemRid](t, m, "select_first")
	text := fn[fnRid](t, m, "text")
	attr := fn[fnMemRid](t, m, "attr")
	size := fn[fnRid](t, m, "size")
	kind := fn[fnRid](t, m, "kind")

	hp, hn := g.str(testPage)
	bp, bn := g.str("https://example.com/a/b")
	doc := parse(bg, g.m, hp, hn, bp, bn)
	if kind(doc) != kindDocument {
		t.Fatalf("kind(doc) = %d", kind(doc))
	}
	q := func(rid int32, s string) int32 { p, n := g.str(s); return sel(bg, g.m, rid, p, n) }
	q1 := func(rid int32, s string) int32 { p, n := g.str(s); return selFirst(bg, g.m, rid, p, n) }
	at := func(rid int32, s string) string { p, n := g.str(s); return strOf(t, h, attr(bg, g.m, rid, p, n)) }

	lis := q(doc, "li.ch")
	if kind(lis) != kindElementList || size(lis) != 3 {
		t.Fatalf("select li: kind %d size %d", kind(lis), size(lis))
	}
	if s := strOf(t, h, text(lis)); s != "One Two Three" {
		t.Errorf("Elements.text = %q", s)
	}
	if s := strOf(t, h, text(q1(doc, "h1"))); s != "Title bold" {
		t.Errorf("h1 text = %q", s)
	}
	p := q1(doc, "p")
	if s := strOf(t, h, text(p)); s != "first line second line" {
		t.Errorf("p text (normalized, br as space) = %q", s)
	}
	if s := strOf(t, h, fn[fnRid](t, m, "untrimmed_text")(p)); s != "first   line second line " {
		t.Errorf("untrimmed_text (SwiftSoup keeps br/block spacing) = %q", s)
	}
	if s := strOf(t, h, fn[fnRid](t, m, "own_text")(q1(doc, "h1"))); s != "Title" {
		t.Errorf("own_text = %q", s)
	}
	if s := strOf(t, h, text(q1(doc, "pre"))); s != "keep   spaces" {
		t.Errorf("pre text = %q", s)
	}
	a := q1(doc, "a")
	if s := at(a, "href"); s != "page/2" {
		t.Errorf("attr href = %q", s)
	}
	if s := at(a, "abs:href"); s != "https://example.com/sub/page/2" {
		t.Errorf("abs:href (with <base>) = %q", s)
	}
	if s := at(a, "missing"); s != "" {
		t.Errorf("missing attr = %q", s)
	}
	if s := at(q(doc, "a, li"), "href"); s != "page/2" {
		t.Errorf("Elements.attr (first having it) = %q", s)
	}
	if s := strOf(t, h, fn[fnRid](t, m, "base_uri")(doc)); s != "https://example.com/sub/" {
		t.Errorf("base_uri = %q", s)
	}
	if s := strOf(t, h, fn[fnRid](t, m, "data")(q1(doc, "head"))); s != "var x = 1;" {
		t.Errorf("data = %q", s)
	}

	// jsoup selector extensions
	if s := strOf(t, h, text(q1(doc, "li:eq(1)"))); s != "Two" {
		t.Errorf(":eq(1) = %q", s)
	}
	if n := size(q(doc, "li:lt(2)")); n != 2 {
		t.Errorf(":lt(2) size = %d", n)
	}
	if s := strOf(t, h, text(q(doc, "li:gt(0)"))); s != "Two Three" {
		t.Errorf(":gt(0) = %q", s)
	}
	if s := strOf(t, h, text(q1(doc, "li:contains(two)"))); s != "Two" {
		t.Errorf(":contains is case-insensitive: %q", s)
	}
	if s := strOf(t, h, text(q1(doc, "a[href^=page/]"))); s != "next" {
		t.Errorf("unquoted attribute value = %q", s)
	}
	if n := size(q(doc, "div:has(h1)")); n != 1 {
		t.Errorf(":has size = %d", n)
	}
	// select includes the root element itself, like jsoup
	div := q1(doc, "#main")
	if n := size(q(div, "div")); n != 1 {
		t.Errorf("select on element includes itself: size %d", n)
	}
	if r := q(doc, "li["); r != htmlInvalidQuery {
		t.Errorf("bad selector = %d", r)
	}
	if r := q1(doc, "table"); r != htmlNoResult {
		t.Errorf("select_first no match = %d", r)
	}

	// Elements accessors
	if s := strOf(t, h, text(fn[fnRid](t, m, "first")(lis))); s != "One" {
		t.Errorf("first = %q", s)
	}
	if s := strOf(t, h, text(fn[fnRid](t, m, "last")(lis))); s != "Three" {
		t.Errorf("last = %q", s)
	}
	get := fn[func(int32, int32) int32](t, m, "get")
	two := get(lis, 1)
	if r := get(lis, 9); r != htmlNoResult {
		t.Errorf("get out of range = %d", r)
	}

	// navigation
	if s := strOf(t, h, text(fn[fnRid](t, m, "next")(two))); s != "Three" {
		t.Errorf("next = %q", s)
	}
	if s := strOf(t, h, text(fn[fnRid](t, m, "previous")(two))); s != "One" {
		t.Errorf("previous = %q", s)
	}
	if n := size(fn[fnRid](t, m, "siblings")(two)); n != 2 {
		t.Errorf("siblings = %d", n)
	}
	if s := strOf(t, h, fn[fnRid](t, m, "tag_name")(fn[fnRid](t, m, "parent")(two))); s != "ul" {
		t.Errorf("parent tag = %q", s)
	}
	if n := size(fn[fnRid](t, m, "children")(q1(doc, "ul"))); n != 3 {
		t.Errorf("children = %d", n)
	}
	nodes := fn[fnRid](t, m, "child_nodes")(div)
	foundComment := false
	for i := int32(0); i < size(nodes); i++ {
		if kind(get(nodes, i)) == kindComment {
			foundComment = true
		}
	}
	if !foundComment {
		t.Error("child_nodes should include the comment node")
	}

	// classes and attributes
	hasClass := fn[fnMemRid](t, m, "has_class")
	cp, cn := g.str("WIDE")
	if hasClass(bg, g.m, div, cp, cn) != 1 {
		t.Error("has_class is case-insensitive")
	}
	if s := strOf(t, h, fn[fnRid](t, m, "class_name")(div)); s != "box  wide" {
		t.Errorf("class_name = %q", s)
	}
	np, nn := g.str("new")
	fn[fnMemRid](t, m, "add_class")(bg, g.m, div, np, nn)
	bxp, bxn := g.str("box")
	fn[fnMemRid](t, m, "remove_class")(bg, g.m, div, bxp, bxn)
	if s := strOf(t, h, fn[fnRid](t, m, "class_name")(div)); s != "wide new" {
		t.Errorf("class_name after add/remove = %q", s)
	}
	if s := strOf(t, h, fn[fnRid](t, m, "id")(div)); s != "main" {
		t.Errorf("id = %q", s)
	}
	kp, kn := g.str("data-x")
	vp, vn := g.str("1")
	fn[fnMemRid2](t, m, "set_attr")(bg, g.m, a, kp, kn, vp, vn)
	if fn[fnMemRid](t, m, "has_attr")(bg, g.m, a, kp, kn) != 1 {
		t.Error("set_attr/has_attr")
	}
	fn[fnMemRid](t, m, "remove_attr")(bg, g.m, a, kp, kn)
	if fn[fnMemRid](t, m, "has_attr")(bg, g.m, a, kp, kn) != 0 {
		t.Error("remove_attr")
	}

	// mutation and serialization
	tp, tn := g.str("<i>new</i> text")
	fn[fnMemRid](t, m, "set_html")(bg, g.m, p, tp, tn)
	if s := strOf(t, h, fn[fnRid](t, m, "html")(p)); s != "<i>new</i> text" {
		t.Errorf("html after set_html = %q", s)
	}
	xp, xn := g.str("<b>!</b>")
	fn[fnMemRid](t, m, "append")(bg, g.m, p, xp, xn)
	fn[fnMemRid](t, m, "prepend")(bg, g.m, p, xp, xn)
	if s := strOf(t, h, fn[fnRid](t, m, "outer_html")(p)); s != "<p><b>!</b><i>new</i> text<b>!</b></p>" {
		t.Errorf("outer_html = %q", s)
	}
	sp, sn := g.str("a < b")
	fn[fnMemRid](t, m, "set_text")(bg, g.m, p, sp, sn)
	if s := strOf(t, h, fn[fnRid](t, m, "html")(p)); s != "a &lt; b" {
		t.Errorf("html after set_text = %q", s)
	}
	fn[fnRid](t, m, "remove")(lis)
	if n := size(q(doc, "li")); n != 0 {
		t.Errorf("remove left %d items", n)
	}

	// escape / unescape / fragments
	ep, en := g.str(`<a & "b">`)
	if s := strOf(t, h, fn[fnMemStr](t, m, "escape")(bg, g.m, ep, en)); s != "&lt;a &amp; &#34;b&#34;&gt;" {
		t.Errorf("escape = %q", s)
	}
	up, un := g.str("&lt;&amp;&nbsp;&#39;")
	if s := strOf(t, h, fn[fnMemStr](t, m, "unescape")(bg, g.m, up, un)); s != "<& '" {
		t.Errorf("unescape = %q", s)
	}
	fp, fln := g.str("<title>T</title><span>x</span>")
	frag := fn[fnMemParse](t, m, "parse_fragment")(bg, g.m, fp, fln, 0, 0)
	if n := size(q(frag, "body > title, body > span")); n != 2 {
		t.Errorf("parse_fragment puts everything in body: %d", n)
	}
	if r := parse(bg, g.m, hp, 0, 0, 0); r != htmlInvalidString {
		t.Errorf("empty html = %d", r)
	}
	if r := text(9999); r != htmlInvalidDescriptor {
		t.Errorf("bad descriptor = %d", r)
	}
}
