package sandboxtest

import "encoding/binary"

// Value types of the wasm binary format.
const (
	I32 byte = 0x7f
	I64 byte = 0x7e
)

// FuncImport is an imported function of a hand-built module.
type FuncImport struct {
	Module, Name    string
	Params, Results []byte
}

// TestModule describes a minimal module for tests that need shapes no real
// toolchain emits: wrong signatures, memory imports, oversized memories.
// The module exports a "handle" function that returns ReturnCode for an i32
// result and zero for an i64 one.
type TestModule struct {
	HandleParams, HandleResults []byte
	FuncImports                 []FuncImport
	// MemoryImport, when set, imports a memory with this module/name and 1 page.
	MemoryImport *FuncImport
	// MemoryMin, when > 0, defines (and exports as "memory") a memory with this minimum.
	MemoryMin uint32
	// ReturnCode is the value of handle's i32 result.
	ReturnCode byte
	// LoopingStart adds a start section whose function never returns.
	LoopingStart bool
}

// Build encodes the module in the wasm binary format.
func (m TestModule) Build() []byte {
	out := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

	// Type section: one type per imported function, then handle's type.
	var types [][]byte
	for _, f := range m.FuncImports {
		types = append(types, funcType(f.Params, f.Results))
	}
	types = append(types, funcType(m.HandleParams, m.HandleResults))
	if m.LoopingStart {
		types = append(types, funcType(nil, nil))
	}
	out = section(out, 1, vec(types))

	var imports [][]byte
	for i, f := range m.FuncImports {
		imp := append(name(f.Module), name(f.Name)...)
		imports = append(imports, append(append(imp, 0x00), uleb(uint64(i))...))
	}
	if m.MemoryImport != nil {
		imp := append(name(m.MemoryImport.Module), name(m.MemoryImport.Name)...)
		imports = append(imports, append(imp, 0x02, 0x00, 0x01))
	}
	if len(imports) > 0 {
		out = section(out, 2, vec(imports))
	}

	handleType := uint64(len(m.FuncImports))
	funcs := [][]byte{uleb(handleType)}
	if m.LoopingStart {
		funcs = append(funcs, uleb(handleType+1))
	}
	out = section(out, 3, vec(funcs))

	if m.MemoryMin > 0 {
		out = section(out, 5, vec([][]byte{append([]byte{0x00}, uleb(uint64(m.MemoryMin))...)}))
	}

	handleIdx := uint64(len(m.FuncImports))
	exports := [][]byte{append(append(name("handle"), 0x00), uleb(handleIdx)...)}
	if m.MemoryMin > 0 {
		exports = append(exports, append(name("memory"), 0x02, 0x00))
	}
	out = section(out, 7, vec(exports))
	if m.LoopingStart {
		out = section(out, 8, uleb(handleIdx+1))
	}

	body := []byte{0x00} // no locals
	for _, r := range m.HandleResults {
		switch r {
		case I32:
			body = append(body, 0x41, m.ReturnCode&0x3f) // i32.const ReturnCode (0..63)
		case I64:
			body = append(body, 0x42, 0x00) // i64.const 0
		}
	}
	body = append(body, 0x0b)
	bodies := [][]byte{append(uleb(uint64(len(body))), body...)}
	if m.LoopingStart {
		loop := []byte{0x00, 0x03, 0x40, 0x0c, 0x00, 0x0b, 0x0b} // loop br 0 end end
		bodies = append(bodies, append(uleb(uint64(len(loop))), loop...))
	}
	out = section(out, 10, vec(bodies))
	return out
}

func funcType(params, results []byte) []byte {
	t := append([]byte{0x60}, uleb(uint64(len(params)))...)
	t = append(t, params...)
	t = append(t, uleb(uint64(len(results)))...)
	return append(t, results...)
}

func section(out []byte, id byte, payload []byte) []byte {
	out = append(out, id)
	out = append(out, uleb(uint64(len(payload)))...)
	return append(out, payload...)
}

func vec(items [][]byte) []byte {
	out := uleb(uint64(len(items)))
	for _, it := range items {
		out = append(out, it...)
	}
	return out
}

func name(s string) []byte { return append(uleb(uint64(len(s))), s...) }

// uleb encodes unsigned LEB128, which is what Go's uvarint format is.
func uleb(v uint64) []byte { return binary.AppendUvarint(nil, v) }
