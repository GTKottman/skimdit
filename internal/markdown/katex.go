// SPDX-License-Identifier: AGPL-3.0-or-later

package markdown

import (
	_ "embed"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"

	"github.com/dop251/goja"
)

//go:embed katex/katex.min.js
var katexJS string

// KaTeXVersion is the bundled KaTeX release.
const KaTeXVersion = "0.18.11"

// KaTeX renders TeX to HTML+MathML at build time with the bundled KaTeX,
// run in a pure-Go JavaScript engine. Results are cached by input.
type KaTeX struct {
	once  sync.Once
	prog  *goja.Program
	err   error
	pool  chan *goja.Runtime
	made  int
	mu    sync.Mutex
	cache sync.Map
}

// NewKaTeX returns a lazy KaTeX engine; nothing loads until the first formula.
func NewKaTeX() *KaTeX { return &KaTeX{} }

type katexKey struct {
	tex     string
	display bool
}

// Render renders one formula.
func (k *KaTeX) Render(tex string, display bool) (string, error) {
	key := katexKey{tex, display}
	if v, ok := k.cache.Load(key); ok {
		return v.(string), nil
	}
	k.once.Do(func() {
		k.prog, k.err = goja.Compile("katex.min.js", katexJS, false)
		k.pool = make(chan *goja.Runtime, runtime.GOMAXPROCS(0))
	})
	if k.err != nil {
		return "", k.err
	}
	vm, err := k.get()
	if err != nil {
		return "", err
	}
	defer func() { k.pool <- vm }()
	if err := vm.Set("__tex", tex); err != nil {
		return "", err
	}
	_ = vm.Set("__display", display)
	v, err := vm.RunString(`katex.renderToString(__tex, {displayMode: __display, output: "htmlAndMathml", throwOnError: true, trust: false, strict: "warn"})`)
	if err != nil {
		var ex *goja.Exception
		if errors.As(err, &ex) {
			msg := ex.Value().String()
			msg = strings.TrimPrefix(msg, "ParseError: ")
			return "", fmt.Errorf("%s", msg)
		}
		return "", err
	}
	out := v.String()
	k.cache.Store(key, out)
	return out, nil
}

func (k *KaTeX) get() (*goja.Runtime, error) {
	select {
	case vm := <-k.pool:
		return vm, nil
	default:
	}
	k.mu.Lock()
	if k.made >= cap(k.pool) {
		k.mu.Unlock()
		return <-k.pool, nil
	}
	k.made++
	k.mu.Unlock()
	vm := goja.New()
	// KaTeX prints warnings through console in strict "warn" mode.
	console := vm.NewObject()
	_ = console.Set("warn", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = console.Set("log", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = vm.Set("console", console)
	if _, err := vm.RunProgram(k.prog); err != nil {
		return nil, fmt.Errorf("loading KaTeX: %w", err)
	}
	return vm, nil
}
