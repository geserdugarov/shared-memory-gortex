package astquery

import (
	sitter "github.com/zzet/gortex/internal/parser/tsitter"
	bashlang "github.com/zzet/gortex/internal/parser/tsitter/bash"
	clang "github.com/zzet/gortex/internal/parser/tsitter/c"
	cpplang "github.com/zzet/gortex/internal/parser/tsitter/cpp"
	csharplang "github.com/zzet/gortex/internal/parser/tsitter/csharp"
	elixirlang "github.com/zzet/gortex/internal/parser/tsitter/elixir"
	golang "github.com/zzet/gortex/internal/parser/tsitter/golang"
	javalang "github.com/zzet/gortex/internal/parser/tsitter/java"
	jslang "github.com/zzet/gortex/internal/parser/tsitter/javascript"
	kotlinlang "github.com/zzet/gortex/internal/parser/tsitter/kotlin"
	phplang "github.com/zzet/gortex/internal/parser/tsitter/php"
	pylang "github.com/zzet/gortex/internal/parser/tsitter/python"
	rubylang "github.com/zzet/gortex/internal/parser/tsitter/ruby"
	rustlang "github.com/zzet/gortex/internal/parser/tsitter/rust"
	scalalang "github.com/zzet/gortex/internal/parser/tsitter/scala"
	tslang "github.com/zzet/gortex/internal/parser/tsitter/typescript"
)

// DefaultLanguageResolver maps the language strings stored on
// KindFile nodes to their tree-sitter binding. The mapping covers
// every language the bundled detectors reference plus a handful of
// extras for raw-pattern queries. Languages that fall outside this
// list return nil — the engine then skips the matching targets
// (ast-grep behaves identically when its grammar isn't available).
//
// To extend: add the import + entry here. The detector definitions
// reference languages by string, not by binding pointer, so adding a
// language here doesn't require touching detectors.go.
func DefaultLanguageResolver(name string) *sitter.Language {
	switch name {
	case "go":
		return golang.GetLanguage()
	case "python":
		return pylang.GetLanguage()
	case "javascript":
		return jslang.GetLanguage()
	case "typescript":
		// NB: tsx grammar is exposed by the typescript binding's
		// `GetTSXLanguage`; for TSX file targets a richer resolver
		// can be passed by the caller. The default uses the plain
		// .ts grammar which handles JSX-free TypeScript only.
		return tslang.GetLanguage()
	case "ruby":
		return rubylang.GetLanguage()
	case "java":
		return javalang.GetLanguage()
	case "kotlin":
		return kotlinlang.GetLanguage()
	case "scala":
		return scalalang.GetLanguage()
	case "rust":
		return rustlang.GetLanguage()
	case "elixir":
		return elixirlang.GetLanguage()
	case "php":
		return phplang.GetLanguage()
	case "c":
		return clang.GetLanguage()
	case "cpp", "c++":
		return cpplang.GetLanguage()
	case "csharp", "c#":
		return csharplang.GetLanguage()
	case "bash", "shell", "sh":
		return bashlang.GetLanguage()
	}
	return nil
}
