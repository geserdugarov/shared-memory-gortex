package graph

// IsContentNode reports whether n is a CONTENT section node — a KindDoc
// chunk tagged data_class="content" (text / pdf / pptx / xlsx section
// bodies). Content bodies are indexed in the dedicated content store
// (ContentSearcher), never the symbol search, and are excluded from the
// code-oriented analysis passes — so this predicate is the single place
// every package agrees on what "content" means. Markdown prose (KindDoc
// without data_class=content) and data assets (data_class="data") are NOT
// content and keep their existing treatment.
func IsContentNode(n *Node) bool {
	if n == nil || n.Kind != KindDoc || n.Meta == nil {
		return false
	}
	dc, _ := n.Meta["data_class"].(string)
	return dc == "content"
}
