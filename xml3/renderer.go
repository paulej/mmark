package xml3

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/gomarkdown/markdown/ast"
	"github.com/gomarkdown/markdown/html"
	"github.com/mmarkdown/mmark/mast"
)

// Flags control optional behavior of XML3 renderer.
type Flags int

// HTML renderer configuration options.
const (
	FlagsNone Flags = iota
	TOC             // Generate a table of contents

	CommonFlags Flags = FlagsNone
)

type RendererOptions struct {
	// Callouts are supported and detected by setting this option to the callout prefix.
	Callout string

	Flags Flags // Flags allow customizing this renderer's behavior
}

// Renderer implements Renderer interface for IETF XMLv3 output. See RFC 7991.
type Renderer struct {
	opts RendererOptions

	documentMatter ast.DocumentMatters // keep track of front/main/back matter
	section        *ast.Heading        // current open section
}

// New creates and configures an Renderer object, which satisfies the Renderer interface.
func NewRenderer(opts RendererOptions) *Renderer {
	return &Renderer{opts: opts}
}

func (r *Renderer) text(w io.Writer, text *ast.Text) {
	if _, parentIsLink := text.Parent.(*ast.Link); parentIsLink {
		//html.EscLink(w, text.Literal)
		r.out(w, text.Literal)
		return
	}
	if heading, parentIsHeading := text.Parent.(*ast.Heading); parentIsHeading {
		if isAbstract(heading.Special) {
			// No <name> when abstract, should output anything
			return
		}
		r.outs(w, "<name>")
		html.EscapeHTML(w, text.Literal)
		r.outs(w, "</name>")
		return
	}

	html.EscapeHTML(w, text.Literal)
}

func (r *Renderer) hardBreak(w io.Writer, node *ast.Hardbreak) {
	r.outs(w, "<br />")
	r.cr(w)
}

func (r *Renderer) strong(w io.Writer, node *ast.Strong, entering bool) {
	// *iff* we have a text node as a child *and* that text is 2119, we output bcp14 tags, otherwise just string.
	text := ast.GetFirstChild(node)
	if t, ok := text.(*ast.Text); ok {
		if is2119(t.Literal) {
			r.outOneOf(w, entering, "<bcp14>", "</bcp14>")
			return
		}
	}

	r.outOneOf(w, entering, "<strong>", "</strong>")
}

func (r *Renderer) matter(w io.Writer, node *ast.DocumentMatter) {
	r.sectionClose(w)
	r.section = nil

	switch node.Matter {
	case ast.DocumentMatterFront:
		r.outs(w, "<front>")
		r.cr(w)
	case ast.DocumentMatterMain:
		r.cr(w)
		r.outs(w, "</front>")
		r.cr(w)
		r.outs(w, "<main>")
	case ast.DocumentMatterBack:
		r.cr(w)
		r.outs(w, "</main>")
		r.cr(w)
		r.outs(w, "<back>")
		r.cr(w)
	}
	r.documentMatter = node.Matter
}

func (r *Renderer) headingEnter(w io.Writer, heading *ast.Heading) {
	var attrs []string
	tag := "<section"
	if heading.Special != nil {
		tag = "<note"
		if isAbstract(heading.Special) {
			tag = "<abstract"
		}
	}

	r.cr(w)
	r.outTag(w, tag, attrs)
}

func (r *Renderer) headingExit(w io.Writer, heading *ast.Heading) {
	r.cr(w)
}

func (r *Renderer) heading(w io.Writer, node *ast.Heading, entering bool) {
	if !entering {
		r.headingExit(w, node)
		return
	}

	r.sectionClose(w)
	r.section = node
	r.headingEnter(w, node)
}

func (r *Renderer) citation(w io.Writer, node *ast.Citation, entering bool) {
	if !entering {
		return
	}
	for _, c := range node.Destination {
		attr := []string{fmt.Sprintf(`target="%s"`, c)}
		r.outTag(w, "<xref", attr)
		r.outs(w, "</xref>")
	}
}

func (r *Renderer) RenderNode(w io.Writer, node ast.Node, entering bool) ast.WalkStatus {
	switch node := node.(type) {
	case *ast.Document:
		// do nothing
	case *mast.Title:
		r.titleBlock(w, node)
	case *ast.Text:
		r.text(w, node)
	case *ast.Softbreak:
		r.cr(w)
	case *ast.Hardbreak:
		r.hardBreak(w, node)
	case *ast.Emph:
		r.outOneOf(w, entering, "<em>", "</em>")
	case *ast.Strong:
		r.strong(w, node, entering)
	case *ast.Del:
		r.outOneOf(w, entering, "<del>", "</del>")
	case *ast.Citation:
		r.citation(w, node, entering)
	case *ast.DocumentMatter:
		if entering {
			r.matter(w, node)
		}
	case *ast.Heading:
		r.heading(w, node, entering)
	case *ast.Paragraph:

	case *ast.HTMLBlock:
	default:
		panic(fmt.Sprintf("Unknown node %T", node))
	}
	return ast.GoToNext
}

// RenderHeader writes HTML document preamble and TOC if requested.
func (r *Renderer) RenderHeader(w io.Writer, ast ast.Node) {
	r.writeDocumentHeader(w)
	if r.opts.Flags&TOC != 0 {
		r.writeTOC(w, ast)
	}
}

// RenderFooter writes HTML document footer.
func (r *Renderer) RenderFooter(w io.Writer, _ ast.Node) {
	r.sectionClose(w)
	r.section = nil

	switch r.documentMatter {
	case ast.DocumentMatterFront:
		r.outs(w, "</front>\n")
	case ast.DocumentMatterMain:
		r.outs(w, "</main>\n")
	case ast.DocumentMatterBack:
		r.outs(w, "</back>\n")
	}
	io.WriteString(w, "\n</rfc>\n")
}

func (r *Renderer) writeDocumentHeader(w io.Writer) {
}

func (r *Renderer) writeTOC(w io.Writer, doc ast.Node) {
	buf := bytes.Buffer{}

	inHeading := false
	tocLevel := 0
	headingCount := 0

	ast.WalkFunc(doc, func(node ast.Node, entering bool) ast.WalkStatus {
		if nodeData, ok := node.(*ast.Heading); ok && !nodeData.IsTitleblock {
			inHeading = entering
			if !entering {
				buf.WriteString("</a>")
				return ast.GoToNext
			}
			nodeData.HeadingID = fmt.Sprintf("toc_%d", headingCount)
			if nodeData.Level == tocLevel {
				buf.WriteString("</li>\n\n<li>")
			} else if nodeData.Level < tocLevel {
				for nodeData.Level < tocLevel {
					tocLevel--
					buf.WriteString("</li>\n</ul>")
				}
				buf.WriteString("</li>\n\n<li>")
			} else {
				for nodeData.Level > tocLevel {
					tocLevel++
					buf.WriteString("\n<ul>\n<li>")
				}
			}

			fmt.Fprintf(&buf, `<a href="#toc_%d">`, headingCount)
			headingCount++
			return ast.GoToNext
		}

		if inHeading {
			return r.RenderNode(&buf, node, entering)
		}

		return ast.GoToNext
	})

	for ; tocLevel > 0; tocLevel-- {
		buf.WriteString("</li>\n</ul>")
	}

	if buf.Len() > 0 {
		io.WriteString(w, "<nav>\n")
		w.Write(buf.Bytes())
		io.WriteString(w, "\n\n</nav>\n")
	}
}

func isList(node ast.Node) bool {
	_, ok := node.(*ast.List)
	return ok
}

func isListTight(node ast.Node) bool {
	if list, ok := node.(*ast.List); ok {
		return list.Tight
	}
	return false
}

func isListItem(node ast.Node) bool {
	_, ok := node.(*ast.ListItem)
	return ok
}

func isListItemTerm(node ast.Node) bool {
	data, ok := node.(*ast.ListItem)
	return ok && data.ListFlags&ast.ListTypeTerm != 0
}

// TODO: move to internal package
func skipSpace(data []byte, i int) int {
	n := len(data)
	for i < n && isSpace(data[i]) {
		i++
	}
	return i
}

// TODO: move to internal package
var validUris = [][]byte{[]byte("http://"), []byte("https://"), []byte("ftp://"), []byte("mailto://")}
var validPaths = [][]byte{[]byte("/"), []byte("./"), []byte("../")}

func isSafeLink(link []byte) bool {
	for _, path := range validPaths {
		if len(link) >= len(path) && bytes.Equal(link[:len(path)], path) {
			if len(link) == len(path) {
				return true
			} else if isAlnum(link[len(path)]) {
				return true
			}
		}
	}

	for _, prefix := range validUris {
		// TODO: handle unicode here
		// case-insensitive prefix test
		if len(link) > len(prefix) && bytes.Equal(bytes.ToLower(link[:len(prefix)]), prefix) && isAlnum(link[len(prefix)]) {
			return true
		}
	}

	return false
}

// TODO: move to internal package
// Create a url-safe slug for fragments
func slugify(in []byte) []byte {
	if len(in) == 0 {
		return in
	}
	out := make([]byte, 0, len(in))
	sym := false

	for _, ch := range in {
		if isAlnum(ch) {
			sym = false
			out = append(out, ch)
		} else if sym {
			continue
		} else {
			out = append(out, '-')
			sym = true
		}
	}
	var a, b int
	var ch byte
	for a, ch = range out {
		if ch != '-' {
			break
		}
	}
	for b = len(out) - 1; b > 0; b-- {
		if out[b] != '-' {
			break
		}
	}
	return out[a : b+1]
}

// TODO: move to internal package
// isAlnum returns true if c is a digit or letter
// TODO: check when this is looking for ASCII alnum and when it should use unicode
func isAlnum(c byte) bool {
	return (c >= '0' && c <= '9') || isLetter(c)
}

// isSpace returns true if c is a white-space charactr
func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}

// isLetter returns true if c is ascii letter
func isLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// isPunctuation returns true if c is a punctuation symbol.
func isPunctuation(c byte) bool {
	for _, r := range []byte("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~") {
		if c == r {
			return true
		}
	}
	return false
}

func blockAttrs(node ast.Node) []string {
	var attr *ast.Attribute
	var s []string
	if c := node.AsContainer(); c != nil && c.Attribute != nil {
		attr = c.Attribute
	}
	if l := node.AsLeaf(); l != nil && l.Attribute != nil {
		attr = l.Attribute
	}
	if attr == nil {
		return nil
	}

	if attr.ID != nil {
		s = append(s, fmt.Sprintf(`id="%s"`, attr.ID))
	}

	for _, c := range attr.Classes {
		s = append(s, fmt.Sprintf(`class="%s"`, c))
	}

	// sort the attributes so it remain stable between runs
	var keys = []string{}
	for k, _ := range attr.Attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s = append(s, fmt.Sprintf(`%s="%s"`, k, attr.Attrs[k]))
	}

	return s
}

func tagWithAttributes(name string, attrs []string) string {
	s := name
	if len(attrs) > 0 {
		s += " " + strings.Join(attrs, " ")
	}
	return s + ">"
}
