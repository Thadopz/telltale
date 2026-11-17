package knowledge_index_pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/document"
	"github.com/cloudwego/eino/schema"
	"github.com/rivo/uniseg"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

const chunkStrategy = "markdown-ast-byte-v1"

type sourceRange struct {
	Start   int  `json:"start"`
	End     int  `json:"end"`
	Overlap bool `json:"overlap,omitempty"`
}

type structuralTransformer struct {
	limit int
}

func (*structuralTransformer) GetType() string {
	return "StructuralMarkdownSplitter"
}

// Goldmark 提供原文位置，UTF-8 字节数提供保守 token 上界。
func (t *structuralTransformer) Transform(ctx context.Context, parents []*schema.Document, opts ...document.TransformerOption) ([]*schema.Document, error) {
	if t.limit < 16 {
		return nil, fmt.Errorf("structural byte limit must be at least 16")
	}
	var result []*schema.Document
	for _, parent := range parents {
		if parent == nil || !utf8.ValidString(parent.Content) {
			return nil, fmt.Errorf("source must be non-nil valid UTF-8")
		}
		source := []byte(parent.Content)
		parsers := parser.DefaultBlockParsers()
		for i := range parsers {
			parsers[i] = util.Prioritized(&positionParser{BlockParser: parsers[i].Value.(parser.BlockParser)}, parsers[i].Priority-1)
		}
		markdownParser := parser.NewParser(
			parser.WithBlockParsers(parsers...),
			parser.WithInlineParsers(parser.DefaultInlineParsers()...),
			parser.WithParagraphTransformers(parser.DefaultParagraphTransformers()...),
		)
		root := goldmark.New(goldmark.WithParser(markdownParser), goldmark.WithExtensions(extension.Table)).Parser().Parse(text.NewReader(source))
		var nodes []ast.Node
		var starts []int
		for node := root.FirstChild(); node != nil; node = node.NextSibling() {
			start := blockStart(node, source)
			if start < 0 {
				return nil, fmt.Errorf("missing source position for %s", node.Kind())
			}
			nodes = append(nodes, node)
			starts = append(starts, start)
		}
		var headings [6]string
		sectionStart, sectionEnd := 0, len(source)
		for i, node := range nodes {
			if _, ok := node.(*ast.Heading); ok {
				sectionEnd = starts[i]
				break
			}
		}
		var pending []sourceRange
		pendingKind := ""
		syntheticPrefix, syntheticSuffix := "", ""
		flush := func(reason string) {
			if len(pending) == 0 {
				return
			}
			var content strings.Builder
			content.WriteString(syntheticPrefix)
			for _, span := range pending {
				content.Write(source[span.Start:span.End])
			}
			content.WriteString(syntheticSuffix)
			meta := maps.Clone(parent.MetaData)
			if meta == nil {
				meta = make(map[string]any)
			}
			var path []string
			for i, heading := range headings {
				key := "title"
				if i > 0 {
					key = fmt.Sprintf("heading%d", i+1)
				}
				delete(meta, key)
				if heading != "" {
					path = append(path, heading)
					meta[key] = heading
				}
			}
			meta["_section_path"] = strings.Join(path, " / ")
			meta["_section_start"], meta["_section_end"] = sectionStart, sectionEnd
			meta["_source_ranges"] = append([]sourceRange(nil), pending...)
			meta["_chunk_strategy"] = chunkStrategy
			meta["_source_hash"] = textHash(parent.Content)
			meta["_block_type"] = pendingKind
			meta["_split_reason"] = reason
			meta["_token_estimator"] = "utf8-byte-upper-bound"
			meta["_synthetic_prefix"] = syntheticPrefix
			meta["_synthetic_suffix"] = syntheticSuffix
			overlap := 0
			for _, span := range pending {
				if span.Overlap {
					overlap += span.End - span.Start
				}
			}
			meta["_overlap_bytes"] = overlap
			wire, _ := json.Marshal(pending)
			id := textHash(fmt.Sprintf("%v\x00%s\x00%s\x00%s\x00%s", meta["_source"], chunkStrategy, meta["_section_path"], wire, content.String()))
			result = append(result, &schema.Document{ID: id, Content: content.String(), MetaData: meta})
			pending = nil
			syntheticPrefix, syntheticSuffix = "", ""
		}
		for i, node := range nodes {
			end := len(source)
			if i+1 < len(starts) {
				end = starts[i+1]
			}
			start := starts[i]
			if heading, ok := node.(*ast.Heading); ok {
				flush("section_boundary")
				sectionStart, sectionEnd = start, len(source)
				for j := i + 1; j < len(nodes); j++ {
					if _, ok := nodes[j].(*ast.Heading); ok {
						sectionEnd = starts[j]
						break
					}
				}
				headings[heading.Level-1] = string(heading.Text(source))
				for j := heading.Level; j < len(headings); j++ {
					headings[j] = ""
				}
			}
			kind := node.Kind().String()
			if end-start > t.limit {
				flush("block_boundary")
				prefix, suffix := "", ""
				if fence, ok := node.(*ast.FencedCodeBlock); ok && fence.Lines().Len() > 0 {
					prefix = "```" + string(fence.Language(source)) + "\n"
					suffix = "\n```\n"
					start = fence.Lines().At(0).Start
					end = fence.Lines().At(fence.Lines().Len() - 1).Stop
				} else if kind == "Table" {
					first := strings.IndexByte(string(source[start:end]), '\n')
					if first >= 0 {
						second := strings.IndexByte(string(source[start+first+1:end]), '\n')
						if second >= 0 {
							prefix = string(source[start : start+first+second+2])
							start += len(prefix)
						}
					}
				}
				available := t.limit - len(prefix) - len(suffix)
				if available < 16 {
					return nil, fmt.Errorf("structural display header exceeds byte budget")
				}
				var overlap *sourceRange
				for start < end {
					capacity := available
					if overlap != nil {
						capacity -= overlap.End - overlap.Start
					}
					cut := min(end, start+capacity)
					for cut > start && cut < end && !utf8.RuneStart(source[cut]) {
						cut--
					}
					reason := "unicode_limit"
					if kind == "Paragraph" {
						state := -1
						rest := string(source[start:end])
						position := start
						for rest != "" {
							var sentence string
							sentence, rest, state = uniseg.FirstSentenceInString(rest, state)
							if position+len(sentence) > cut {
								break
							}
							position += len(sentence)
						}
						if position > start {
							cut, reason = position, "sentence_boundary"
						}
					} else if kind == "List" {
						boundary := start
						for item := node.FirstChild(); item != nil; item = item.NextSibling() {
							position := blockStart(item, source)
							if position > boundary && position <= cut {
								boundary = position
							}
						}
						if boundary > start {
							cut, reason = boundary, "list_item_boundary"
						} else if line := strings.LastIndexByte(string(source[start:cut]), '\n'); line >= 0 {
							cut, reason = start+line+1, "line_boundary"
						}
					} else if line := strings.LastIndexByte(string(source[start:cut]), '\n'); line >= 0 {
						cut, reason = start+line+1, "line_boundary"
					}
					pending = []sourceRange{{Start: start, End: cut}}
					if overlap != nil {
						pending = append([]sourceRange{*overlap}, pending...)
					}
					syntheticPrefix, syntheticSuffix = prefix, suffix
					pendingKind = kind
					flush(reason)
					overlap = nil
					if kind == "Paragraph" && reason == "sentence_boundary" {
						rest, state, position := string(source[start:cut]), -1, start
						for rest != "" {
							var sentence string
							sentence, rest, state = uniseg.FirstSentenceInString(rest, state)
							if rest == "" && len(sentence) <= min(48, 384*15/100) {
								overlap = &sourceRange{Start: position, End: cut, Overlap: true}
							}
							position += len(sentence)
						}
					}
					start = cut
				}
				continue
			}
			size := end - start
			for _, span := range pending {
				size += span.End - span.Start
			}
			if size > min(384, t.limit) {
				flush("target_size")
			}
			pending = append(pending, sourceRange{Start: start, End: end})
			pendingKind = kind
		}
		flush("section_end")
	}
	return result, nil
}

func blockStart(node ast.Node, source []byte) int {
	start := -1
	if position, ok := node.AttributeString("source_start"); ok {
		start = position.(int)
		if _, heading := node.(*ast.Heading); !heading || node.Lines().Len() == 0 {
			return start
		}
		return min(start, strings.LastIndexByte(string(source[:node.Lines().At(0).Start]), '\n')+1)
	}
	if node.Type() == ast.TypeBlock && node.Lines().Len() > 0 {
		start = node.Lines().At(0).Start
	}
	if fence, ok := node.(*ast.FencedCodeBlock); ok {
		if fence.Info != nil {
			start = fence.Info.Segment.Start
		} else if start > 0 {
			start = strings.LastIndexByte(string(source[:start-1]), '\n') + 1
		}
	}
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		position := blockStart(child, source)
		if position >= 0 && (start < 0 || position < start) {
			start = position
		}
	}
	if start >= 0 {
		start = strings.LastIndexByte(string(source[:start]), '\n') + 1
	}
	return start
}

type positionParser struct {
	parser.BlockParser
}

func (p *positionParser) Open(parent ast.Node, reader text.Reader, context parser.Context) (ast.Node, parser.State) {
	_, segment := reader.Position()
	node, state := p.BlockParser.Open(parent, reader, context)
	if node != nil {
		node.SetAttributeString("source_start", segment.Start)
	}
	return node, state
}
