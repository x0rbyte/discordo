// Package markdown defines a renderer for tview style tags.
package markdown

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/ayn2op/discordo/internal/config"
	"github.com/diamondburned/ningen/v3/discordmd"
	"github.com/yuin/goldmark/ast"
	gmr "github.com/yuin/goldmark/renderer"
)

type Renderer struct {
	theme config.MessagesListTheme

	listIx     *int
	listNested int
}

func NewRenderer(theme config.MessagesListTheme) *Renderer {
	return &Renderer{theme: theme}
}

func (r *Renderer) AddOptions(opts ...gmr.Option) {}

func (r *Renderer) Render(w io.Writer, source []byte, node ast.Node) error {
	return ast.Walk(node, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		switch node := node.(type) {
		case *ast.Document:
		// noop
		case *ast.Heading:
			r.renderHeading(w, node, entering)
		case *ast.Text:
			r.renderText(w, node, entering, source)
		case *ast.FencedCodeBlock:
			r.renderFencedCodeBlock(w, node, entering, source)
		case *ast.AutoLink:
			r.renderAutoLink(w, node, entering, source)
		case *ast.Link:
			r.renderLink(w, node, entering)
		case *ast.List:
			r.renderList(w, node, entering)
		case *ast.ListItem:
			r.renderListItem(w, entering)

		case *discordmd.Inline:
			r.renderInline(w, node, entering)
		case *discordmd.Mention:
			r.renderMention(w, node, entering)
		case *discordmd.Emoji:
			r.renderEmoji(w, node, entering)
		}

		return ast.WalkContinue, nil
	})
}

func (r *Renderer) renderHeading(w io.Writer, node *ast.Heading, entering bool) {
	if entering {
		io.WriteString(w, strings.Repeat("#", node.Level))
		io.WriteString(w, " ")
	} else {
		io.WriteString(w, "\n")
	}
}

func (r *Renderer) renderFencedCodeBlock(w io.Writer, node *ast.FencedCodeBlock, entering bool, source []byte) {
	io.WriteString(w, "\n")

	if entering {
		// Get language name
		var langName string
		if l := node.Language(source); l != nil {
			langName = string(l)
		}

		// Top border
		io.WriteString(w, "[::d]╭─")
		if langName != "" {
			io.WriteString(w, "[ [::b]")
			io.WriteString(w, langName)
			io.WriteString(w, "[::B] ][::D]")
		} else {
			io.WriteString(w, "──")
		}
		io.WriteString(w, "──────[::D]\n")

		// Get all code content
		lines := node.Lines()
		var codeContent strings.Builder
		for i := range lines.Len() {
			line := lines.At(i)
			codeContent.Write(line.Value(source))
		}

		// Try to get lexer for the language
		lexer := lexers.Get(langName)
		if lexer == nil {
			lexer = lexers.Fallback
		}
		lexer = chroma.Coalesce(lexer)

		// Get a terminal-friendly style
		style := styles.Get("monokai")
		if style == nil {
			style = styles.Fallback
		}

		// Tokenize the code
		iterator, err := lexer.Tokenise(nil, codeContent.String())
		if err != nil {
			// Fallback: just print without highlighting
			r.renderCodeWithoutHighlight(w, codeContent.String())
			return
		}

		// Render highlighted code
		r.renderHighlightedCode(w, iterator, style)

		// Bottom border
		io.WriteString(w, "[::d]╰")
		io.WriteString(w, strings.Repeat("─", 40))
		io.WriteString(w, "[::D]\n")
	}
}

func (r *Renderer) renderCodeWithoutHighlight(w io.Writer, code string) {
	lines := strings.Split(code, "\n")
	for _, line := range lines {
		io.WriteString(w, "[::d]│[::D] ")
		io.WriteString(w, line)
		if line != "" {
			io.WriteString(w, "\n")
		}
	}
}

func (r *Renderer) renderHighlightedCode(w io.Writer, iterator chroma.Iterator, style *chroma.Style) {
	currentLine := strings.Builder{}

	for token := iterator(); token != chroma.EOF; token = iterator() {
		color := r.tokenTypeToColor(token.Type, style)
		value := token.Value

		// Split by newlines to handle multi-line tokens
		lines := strings.Split(value, "\n")
		for i, line := range lines {
			if i > 0 {
				// End current line and start new one
				io.WriteString(w, "[::d]│[::D] ")
				io.WriteString(w, currentLine.String())
				io.WriteString(w, "[-]\n")
				currentLine.Reset()
			}

			if line != "" {
				if color != "" {
					currentLine.WriteString("[" + color + "]")
					currentLine.WriteString(line)
					currentLine.WriteString("[-]")
				} else {
					currentLine.WriteString(line)
				}
			}
		}
	}

	// Write final line if not empty
	if currentLine.Len() > 0 {
		io.WriteString(w, "[::d]│[::D] ")
		io.WriteString(w, currentLine.String())
		io.WriteString(w, "[-]\n")
	}
}

func (r *Renderer) tokenTypeToColor(tokenType chroma.TokenType, style *chroma.Style) string {
	// Map chroma token types to terminal colors
	switch tokenType {
	case chroma.Keyword, chroma.KeywordConstant, chroma.KeywordDeclaration,
		 chroma.KeywordNamespace, chroma.KeywordPseudo, chroma.KeywordReserved,
		 chroma.KeywordType:
		return "lightblue"
	case chroma.Name, chroma.NameAttribute, chroma.NameBuiltin,
		 chroma.NameBuiltinPseudo, chroma.NameClass, chroma.NameConstant,
		 chroma.NameDecorator, chroma.NameEntity, chroma.NameException,
		 chroma.NameFunction, chroma.NameFunctionMagic, chroma.NameLabel,
		 chroma.NameNamespace, chroma.NameOther, chroma.NameProperty,
		 chroma.NameTag, chroma.NameVariable, chroma.NameVariableClass,
		 chroma.NameVariableGlobal, chroma.NameVariableInstance, chroma.NameVariableMagic:
		return "white"
	case chroma.LiteralString, chroma.LiteralStringAffix, chroma.LiteralStringAtom,
		 chroma.LiteralStringBacktick, chroma.LiteralStringBoolean, chroma.LiteralStringChar,
		 chroma.LiteralStringDelimiter, chroma.LiteralStringDoc, chroma.LiteralStringDouble,
		 chroma.LiteralStringEscape, chroma.LiteralStringHeredoc, chroma.LiteralStringInterpol,
		 chroma.LiteralStringName, chroma.LiteralStringOther, chroma.LiteralStringRegex,
		 chroma.LiteralStringSingle, chroma.LiteralStringSymbol:
		return "yellow"
	case chroma.LiteralNumber, chroma.LiteralNumberBin, chroma.LiteralNumberFloat,
		 chroma.LiteralNumberHex, chroma.LiteralNumberInteger, chroma.LiteralNumberIntegerLong,
		 chroma.LiteralNumberOct:
		return "lightmagenta"
	case chroma.Operator, chroma.OperatorWord:
		return "lightcyan"
	case chroma.Comment, chroma.CommentHashbang, chroma.CommentMultiline,
		 chroma.CommentSingle, chroma.CommentSpecial, chroma.CommentPreproc,
		 chroma.CommentPreprocFile:
		return "green::d"
	case chroma.Generic, chroma.GenericDeleted, chroma.GenericEmph, chroma.GenericError,
		 chroma.GenericHeading, chroma.GenericInserted, chroma.GenericOutput,
		 chroma.GenericPrompt, chroma.GenericStrong, chroma.GenericSubheading,
		 chroma.GenericTraceback, chroma.GenericUnderline:
		return "white"
	default:
		return ""
	}
}

func (r *Renderer) renderAutoLink(w io.Writer, node *ast.AutoLink, entering bool, source []byte) {
	urlStyle := r.theme.URLStyle

	if entering {
		fg := urlStyle.GetForeground()
		bg := urlStyle.GetBackground()
		fmt.Fprintf(w, "[%s:%s]", fg, bg)
		w.Write(node.URL(source))
	} else {
		io.WriteString(w, "[-:-]")
	}
}

func (r *Renderer) renderLink(w io.Writer, node *ast.Link, entering bool) {
	urlStyle := r.theme.URLStyle
	if entering {
		fg := urlStyle.GetForeground()
		bg := urlStyle.GetBackground()
		fmt.Fprintf(w, "[%s:%s::%s]", fg, bg, node.Destination)
	} else {
		io.WriteString(w, "[-:-::-]")
	}
}

func (r *Renderer) renderList(w io.Writer, node *ast.List, entering bool) {
	if node.IsOrdered() {
		r.listIx = &node.Start
	} else {
		r.listIx = nil
	}

	if entering {
		io.WriteString(w, "\n")
		r.listNested++
	} else {
		r.listNested--
	}
}

func (r *Renderer) renderListItem(w io.Writer, entering bool) {
	if entering {
		io.WriteString(w, strings.Repeat("  ", r.listNested-1))

		if r.listIx != nil {
			io.WriteString(w, strconv.Itoa(*r.listIx))
			io.WriteString(w, ". ")
			*r.listIx++
		} else {
			io.WriteString(w, "- ")
		}
	} else {
		io.WriteString(w, "\n")
	}
}

func (r *Renderer) renderText(w io.Writer, node *ast.Text, entering bool, source []byte) {
	if entering {
		w.Write(node.Segment.Value(source))
		switch {
		case node.HardLineBreak():
			io.WriteString(w, "\n\n")
		case node.SoftLineBreak():
			io.WriteString(w, "\n")
		}
	}
}
func (r *Renderer) renderInline(w io.Writer, node *discordmd.Inline, entering bool) {
	if start, end := attrToTag(node.Attr); entering && start != "" {
		io.WriteString(w, start)
	} else {
		io.WriteString(w, end)
	}
}

func (r *Renderer) renderMention(w io.Writer, node *discordmd.Mention, entering bool) {
	mentionStyle := r.theme.MentionStyle
	if entering {
		fg := mentionStyle.GetForeground()
		bg := mentionStyle.GetBackground()
		fmt.Fprintf(w, "[%s:%s:b]", fg, bg)

		switch {
		case node.Channel != nil:
			io.WriteString(w, "#"+node.Channel.Name)
		case node.GuildUser != nil:
			name := node.GuildUser.DisplayOrUsername()
			if member := node.GuildUser.Member; member != nil && member.Nick != "" {
				name = member.Nick
			}

			io.WriteString(w, "@"+name)
		case node.GuildRole != nil:
			io.WriteString(w, "@"+node.GuildRole.Name)
		}
	} else {
		io.WriteString(w, "[-:-:B]")
	}
}

func (r *Renderer) renderEmoji(w io.Writer, node *discordmd.Emoji, entering bool) {
	if entering {
		emojiStyle := r.theme.EmojiStyle
		fg := emojiStyle.GetForeground()
		bg := emojiStyle.GetBackground()
		fmt.Fprintf(w, "[%s:%s]", fg, bg)
		io.WriteString(w, ":"+node.Name+":")
	} else {
		io.WriteString(w, "[-:-]")
	}
}

func attrToTag(attr discordmd.Attribute) (string, string) {
	switch attr {
	case discordmd.AttrBold:
		return "[::b]", "[::B]"
	case discordmd.AttrItalics:
		return "[::i]", "[::I]"
	case discordmd.AttrUnderline:
		return "[::u]", "[::U]"
	case discordmd.AttrStrikethrough:
		return "[::s]", "[::S]"
	case discordmd.AttrMonospace:
		return "[::r]", "[::R]"
	default:
		return "", ""
	}
}
