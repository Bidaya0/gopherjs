//go:build js
// +build js

package reflect

import (
	"errors"
	"runtime"
	"strconv"
	"unsafe"

	"internal/itoa"

	"github.com/gopherjs/gopherjs/js"
)

var initialized = false

func init() {
	// avoid dead code elimination
	used := func(i interface{}) {}
	used(rtype{})
	used(uncommonType{})
	used(method{})
	used(arrayType{})
	used(chanType{})
	used(funcType{})
	used(interfaceType{})
	used(mapType{})
	used(ptrType{})
	used(sliceType{})
	used(structType{})
	used(imethod{})
	used(structField{})

	initialized = true
	uint8Type = TypeOf(uint8(0)).(*rtype) // set for real
}

// New returns a Value representing a pointer to a new zero value
// for the specified type. That is, the returned Value's Type is PtrTo(typ).
//
// The upstream version includes an extra check to avoid creating types that
// are tagged as go:notinheap. This shouldn't matter in GopherJS, and tracking
// that state is over-complex, so we just skip that check.
func New(typ Type) Value {
	if typ == nil {
		panic("reflect: New(nil)")
	}
	t := typ.(*rtype)
	pt := t.ptrTo()
	ptr := unsafe_New(t)
	fl := flag(Ptr)
	return Value{pt, ptr, fl}
}

func jsType(typ Type) *js.Object {
	return js.InternalObject(typ).Get("jsType")
}

func reflectType(typ *js.Object) *rtype {
	if typ.Get("reflectType") == js.Undefined {
		rt := &rtype{
			size: uintptr(typ.Get("size").Int()),
			kind: uint8(typ.Get("kind").Int()),
			str:  newNameOff(newName(internalStr(typ.Get("string")), "", typ.Get("exported").Bool())),
		}
		js.InternalObject(rt).Set("jsType", typ)
		typ.Set("reflectType", js.InternalObject(rt))

		methodSet := js.Global.Call("$methodSet", typ)
		if methodSet.Length() != 0 || typ.Get("named").Bool() {
			rt.tflag |= tflagUncommon
			if typ.Get("named").Bool() {
				rt.tflag |= tflagNamed
			}
			var reflectMethods []method
			for i := 0; i < methodSet.Length(); i++ { // Exported methods first.
				m := methodSet.Index(i)
				exported := internalStr(m.Get("pkg")) == ""
				if !exported {
					continue
				}
				reflectMethods = append(reflectMethods, method{
					name: newNameOff(newMethodName(m)),
					mtyp: newTypeOff(reflectType(m.Get("typ"))),
				})
			}
			xcount := uint16(len(reflectMethods))
			for i := 0; i < methodSet.Length(); i++ { // Unexported methods second.
				m := methodSet.Index(i)
				exported := internalStr(m.Get("pkg")) == ""
				if exported {
					continue
				}
				reflectMethods = append(reflectMethods, method{
					name: newNameOff(newMethodName(m)),
					mtyp: newTypeOff(reflectType(m.Get("typ"))),
				})
			}
			ut := &uncommonType{
				pkgPath:  newNameOff(newName(internalStr(typ.Get("pkg")), "", false)),
				mcount:   uint16(methodSet.Length()),
				xcount:   xcount,
				_methods: reflectMethods,
			}
			uncommonTypeMap[rt] = ut
			js.InternalObject(ut).Set("jsType", typ)
		}

		switch rt.Kind() {
		case Array:
			setKindType(rt, &arrayType{
				elem: reflectType(typ.Get("elem")),
				len:  uintptr(typ.Get("len").Int()),
			})
		case Chan:
			dir := BothDir
			if typ.Get("sendOnly").Bool() {
				dir = SendDir
			}
			if typ.Get("recvOnly").Bool() {
				dir = RecvDir
			}
			setKindType(rt, &chanType{
				elem: reflectType(typ.Get("elem")),
				dir:  uintptr(dir),
			})
		case Func:
			params := typ.Get("params")
			in := make([]*rtype, params.Length())
			for i := range in {
				in[i] = reflectType(params.Index(i))
			}
			results := typ.Get("results")
			out := make([]*rtype, results.Length())
			for i := range out {
				out[i] = reflectType(results.Index(i))
			}
			outCount := uint16(results.Length())
			if typ.Get("variadic").Bool() {
				outCount |= 1 << 15
			}
			setKindType(rt, &funcType{
				rtype:    *rt,
				inCount:  uint16(params.Length()),
				outCount: outCount,
				_in:      in,
				_out:     out,
			})
		case Interface:
			methods := typ.Get("methods")
			imethods := make([]imethod, methods.Length())
			for i := range imethods {
				m := methods.Index(i)
				imethods[i] = imethod{
					name: newNameOff(newMethodName(m)),
					typ:  newTypeOff(reflectType(m.Get("typ"))),
				}
			}
			setKindType(rt, &interfaceType{
				rtype:   *rt,
				pkgPath: newName(internalStr(typ.Get("pkg")), "", false),
				methods: imethods,
			})
		case Map:
			setKindType(rt, &mapType{
				key:  reflectType(typ.Get("key")),
				elem: reflectType(typ.Get("elem")),
			})
		case Ptr:
			setKindType(rt, &ptrType{
				elem: reflectType(typ.Get("elem")),
			})
		case Slice:
			setKindType(rt, &sliceType{
				elem: reflectType(typ.Get("elem")),
			})
		case Struct:
			fields := typ.Get("fields")
			reflectFields := make([]structField, fields.Length())
			for i := range reflectFields {
				f := fields.Index(i)
				offsetEmbed := uintptr(i) << 1
				if f.Get("embedded").Bool() {
					offsetEmbed |= 1
				}
				reflectFields[i] = structField{
					name:        newName(internalStr(f.Get("name")), internalStr(f.Get("tag")), f.Get("exported").Bool()),
					typ:         reflectType(f.Get("typ")),
					offsetEmbed: offsetEmbed,
				}
			}
			setKindType(rt, &structType{
				rtype:   *rt,
				pkgPath: newName(internalStr(typ.Get("pkgPath")), "", false),
				fields:  reflectFields,
			})
		}
	}

	return (*rtype)(unsafe.Pointer(typ.Get("reflectType").Unsafe()))
}

func setKindType(rt *rtype, kindType interface{}) {
	js.InternalObject(rt).Set("kindType", js.InternalObject(kindType))
	js.InternalObject(kindType).Set("rtype", js.InternalObject(rt))
}

type uncommonType struct {
	pkgPath nameOff
	mcount  uint16
	xcount  uint16
	moff    uint32

	_methods []method
}

func (t *uncommonType) methods() []method {
	return t._methods
}

func (t *uncommonType) exportedMethods() []method {
	return t._methods[:t.xcount:t.xcount]
}

var uncommonTypeMap = make(map[*rtype]*uncommonType)

func (t *rtype) uncommon() *uncommonType {
	return uncommonTypeMap[t]
}

type funcType struct {
	rtype    `reflect:"func"`
	inCount  uint16
	outCount uint16

	_in  []*rtype
	_out []*rtype
}

func (t *funcType) in() []*rtype {
	return t._in
}

func (t *funcType) out() []*rtype {
	return t._out
}

type name struct {
	bytes *byte
}

type nameData struct {
	name     string
	tag      string
	exported bool
	pkgPath  string
}

var nameMap = make(map[*byte]*nameData)

func (n name) name() (s string) { return nameMap[n.bytes].name }
func (n name) tag() (s string)  { return nameMap[n.bytes].tag }
func (n name) pkgPath() string  { return nameMap[n.bytes].pkgPath }
func (n name) isExported() bool { return nameMap[n.bytes].exported }

func newName(n, tag string, exported bool) name {
	b := new(byte)
	nameMap[b] = &nameData{
		name:     n,
		tag:      tag,
		exported: exported,
	}
	return name{
		bytes: b,
	}
}

// newMethodName creates name instance for a method.
//
// Input object is expected to be an entry of the "methods" list of the
// corresponding JS type.
func newMethodName(m *js.Object) name {
	b := new(byte)
	nameMap[b] = &nameData{
		name:     internalStr(m.Get("name")),
		tag:      "",
		pkgPath:  internalStr(m.Get("pkg")),
		exported: internalStr(m.Get("pkg")) == "",
	}
	return name{bytes: b}
}

var nameOffList []name

func (t *rtype) nameOff(off nameOff) name {
	return nameOffList[int(off)]
}

func newNameOff(n name) nameOff {
	i := len(nameOffList)
	nameOffList = append(nameOffList, n)
	return nameOff(i)
}

var typeOffList []*rtype

func (t *rtype) typeOff(off typeOff) *rtype {
	return typeOffList[int(off)]
}

func newTypeOff(t *rtype) typeOff {
	i := len(typeOffList)
	typeOffList = append(typeOffList, t)
	return typeOff(i)
}

// addReflectOff adds a pointer to the reflection lookup map in the runtime.
// It returns a new ID that can be used as a typeOff or textOff, and will
// be resolved correctly. Implemented in the runtime package.
func addReflectOff(ptr unsafe.Pointer) int32 {
	i := len(typeOffList)
	typeOffList = append(typeOffList, (*rtype)(ptr))
	return int32(i)
}

func internalStr(strObj *js.Object) string {
	var c struct{ str string }
	js.InternalObject(c).Set("str", strObj) // get string without internalizing
	return c.str
}

func isWrapped(typ Type) bool {
	return jsType(typ).Get("wrapped").Bool()
}

func copyStruct(dst, src *js.Object, typ Type) {
	fields := jsType(typ).Get("fields")
	for i := 0; i < fields.Length(); i++ {
		prop := fields.Index(i).Get("prop").String()
		dst.Set(prop, src.Get(prop))
	}
}

func makeValue(t Type, v *js.Object, fl flag) Value {
	rt := t.common()
	if t.Kind() == Array || t.Kind() == Struct || t.Kind() == Ptr {
		return Value{rt, unsafe.Pointer(v.Unsafe()), fl | flag(t.Kind())}
	}
	return Value{rt, unsafe.Pointer(js.Global.Call("$newDataPointer", v, jsType(rt.ptrTo())).Unsafe()), fl | flag(t.Kind()) | flagIndir}
}

func MakeSlice(typ Type, len, cap int) Value {
	if typ.Kind() != Slice {
		panic("reflect.MakeSlice of non-slice type")
	}
	if len < 0 {
		panic("reflect.MakeSlice: negative len")
	}
	if cap < 0 {
		panic("reflect.MakeSlice: negative cap")
	}
	if len > cap {
		panic("reflect.MakeSlice: len > cap")
	}

	return makeValue(typ, js.Global.Call("$makeSlice", jsType(typ), len, cap, js.InternalObject(func() *js.Object { return jsType(typ.Elem()).Call("zero") })), 0)
}

func TypeOf(i interface{}) Type {
	if !initialized { // avoid error of uint8Type
		return &rtype{}
	}
	if i == nil {
		return nil
	}
	return reflectType(js.InternalObject(i).Get("constructor"))
}

func ValueOf(i interface{}) Value {
	if i == nil {
		return Value{}
	}
	return makeValue(reflectType(js.InternalObject(i).Get("constructor")), js.InternalObject(i).Get("$val"), 0)
}

func ArrayOf(count int, elem Type) Type {
	if count < 0 {
		panic("reflect: negative length passed to ArrayOf")
	}

	return reflectType(js.Global.Call("$arrayType", jsType(elem), count))
}

func ChanOf(dir ChanDir, t Type) Type {
	return reflectType(js.Global.Call("$chanType", jsType(t), dir == SendDir, dir == RecvDir))
}

func FuncOf(in, out []Type, variadic bool) Type {
	if variadic && (len(in) == 0 || in[len(in)-1].Kind() != Slice) {
		panic("reflect.FuncOf: last arg of variadic func must be slice")
	}

	jsIn := make([]*js.Object, len(in))
	for i, v := range in {
		jsIn[i] = jsType(v)
	}
	jsOut := make([]*js.Object, len(out))
	for i, v := range out {
		jsOut[i] = jsType(v)
	}
	return reflectType(js.Global.Call("$funcType", jsIn, jsOut, variadic))
}

func MapOf(key, elem Type) Type {
	switch key.Kind() {
	case Func, Map, Slice:
		panic("reflect.MapOf: invalid key type " + key.String())
	}

	return reflectType(js.Global.Call("$mapType", jsType(key), jsType(elem)))
}

func (t *rtype) ptrTo() *rtype {
	return reflectType(js.Global.Call("$ptrType", jsType(t)))
}

func SliceOf(t Type) Type {
	return reflectType(js.Global.Call("$sliceType", jsType(t)))
}

func StructOf(fields []StructField) Type {
	var (
		jsFields  = make([]*js.Object, len(fields))
		fset      = map[string]struct{}{}
		pkgpath   string
		hasGCProg bool
	)
	for i, field := range fields {
		if field.Name == "" {
			panic("reflect.StructOf: field " + strconv.Itoa(i) + " has no name")
		}
		if !isValidFieldName(field.Name) {
			panic("reflect.StructOf: field " + strconv.Itoa(i) + " has invalid name")
		}
		if field.Type == nil {
			panic("reflect.StructOf: field " + strconv.Itoa(i) + " has no type")
		}
		f, fpkgpath := runtimeStructField(field)
		ft := f.typ
		if ft.kind&kindGCProg != 0 {
			hasGCProg = true
		}
		if fpkgpath != "" {
			if pkgpath == "" {
				pkgpath = fpkgpath
			} else if pkgpath != fpkgpath {
				panic("reflect.Struct: fields with different PkgPath " + pkgpath + " and " + fpkgpath)
			}
		}
		name := field.Name
		if f.embedded() {
			// Embedded field
			if field.Type.Kind() == Ptr {
				// Embedded ** and *interface{} are illegal
				elem := field.Type.Elem()
				if k := elem.Kind(); k == Ptr || k == Interface {
					panic("reflect.StructOf: illegal anonymous field type " + field.Type.String())
				}
			}
			switch field.Type.Kind() {
			case Interface:
			case Ptr:
				ptr := (*ptrType)(unsafe.Pointer(ft))
				if unt := ptr.uncommon(); unt != nil {
					if i > 0 && unt.mcount > 0 {
						// Issue 15924.
						panic("reflect: embedded type with methods not implemented if type is not first field")
					}
					if len(fields) > 1 {
						panic("reflect: embedded type with methods not implemented if there is more than one field")
					}
				}
			default:
				if unt := ft.uncommon(); unt != nil {
					if i > 0 && unt.mcount > 0 {
						// Issue 15924.
						panic("reflect: embedded type with methods not implemented if type is not first field")
					}
					if len(fields) > 1 && ft.kind&kindDirectIface != 0 {
						panic("reflect: embedded type with methods not implemented for non-pointer type")
					}
				}
			}
		}

		if _, dup := fset[name]; dup && name != "_" {
			panic("reflect.StructOf: duplicate field " + name)
		}
		fset[name] = struct{}{}
		// To be consistent with Compiler's behavior we need to avoid externalizing
		// the "name" property. The line below is effectively an inverse of the
		// internalStr() function.
		jsf := js.InternalObject(struct{ name string }{name})
		// The rest is set through the js.Object() interface, which the compiler will
		// externalize for us.
		jsf.Set("prop", name)
		jsf.Set("exported", f.name.isExported())
		jsf.Set("typ", jsType(field.Type))
		jsf.Set("tag", field.Tag)
		jsf.Set("embedded", field.Anonymous)
		jsFields[i] = jsf
	}
	_ = hasGCProg
	typ := js.Global.Call("$structType", "", jsFields)
	if pkgpath != "" {
		typ.Set("pkgPath", pkgpath)
	}
	return reflectType(typ)
}

func Zero(typ Type) Value {
	return makeValue(typ, jsType(typ).Call("zero"), 0)
}

func unsafe_New(typ *rtype) unsafe.Pointer {
	switch typ.Kind() {
	case Struct:
		return unsafe.Pointer(jsType(typ).Get("ptr").New().Unsafe())
	case Array:
		return unsafe.Pointer(jsType(typ).Call("zero").Unsafe())
	default:
		return unsafe.Pointer(js.Global.Call("$newDataPointer", jsType(typ).Call("zero"), jsType(typ.ptrTo())).Unsafe())
	}
}

func makeInt(f flag, bits uint64, t Type) Value {
	typ := t.common()
	ptr := unsafe_New(typ)
	switch typ.Kind() {
	case Int8:
		*(*int8)(ptr) = int8(bits)
	case Int16:
		*(*int16)(ptr) = int16(bits)
	case Int, Int32:
		*(*int32)(ptr) = int32(bits)
	case Int64:
		*(*int64)(ptr) = int64(bits)
	case Uint8:
		*(*uint8)(ptr) = uint8(bits)
	case Uint16:
		*(*uint16)(ptr) = uint16(bits)
	case Uint, Uint32, Uintptr:
		*(*uint32)(ptr) = uint32(bits)
	case Uint64:
		*(*uint64)(ptr) = uint64(bits)
	}
	return Value{typ, ptr, f | flagIndir | flag(typ.Kind())}
}

func MakeFunc(typ Type, fn func(args []Value) (results []Value)) Value {
	if typ.Kind() != Func {
		panic("reflect: call of MakeFunc with non-Func type")
	}

	t := typ.common()
	ftyp := (*funcType)(unsafe.Pointer(t))

	fv := js.MakeFunc(func(this *js.Object, arguments []*js.Object) interface{} {
		// Convert raw JS arguments into []Value the user-supplied function expects.
		args := make([]Value, ftyp.NumIn())
		for i := range args {
			argType := ftyp.In(i).common()
			args[i] = makeValue(argType, arguments[i], 0)
		}

		// Call the user-supplied function.
		resultsSlice := fn(args)

		// Verify that returned value types are compatible with the function type specified by the caller.
		if want, got := ftyp.NumOut(), len(resultsSlice); want != got {
			panic("reflect: expected " + strconv.Itoa(want) + " return values, got " + strconv.Itoa(got))
		}
		for i, rtyp := range ftyp.out() {
			if !resultsSlice[i].Type().AssignableTo(rtyp) {
				panic("reflect: " + strconv.Itoa(i) + " return value type is not compatible with the function declaration")
			}
		}

		// Rearrange return values according to the expected function signature.
		switch ftyp.NumOut() {
		case 0:
			return nil
		case 1:
			return resultsSlice[0].object()
		default:
			results := js.Global.Get("Array").New(ftyp.NumOut())
			for i, r := range resultsSlice {
				results.SetIndex(i, r.object())
			}
			return results
		}
	})

	return Value{t, unsafe.Pointer(fv.Unsafe()), flag(Func)}
}

func typedmemmove(t *rtype, dst, src unsafe.Pointer) {
	js.InternalObject(dst).Call("$set", js.InternalObject(src).Call("$get"))
}

func loadScalar(p unsafe.Pointer, n uintptr) uintptr {
	return js.InternalObject(p).Call("$get").Unsafe()
}

func makechan(typ *rtype, size int) (ch unsafe.Pointer) {
	ctyp := (*chanType)(unsafe.Pointer(typ))
	return unsafe.Pointer(js.Global.Get("$Chan").New(jsType(ctyp.elem), size).Unsafe())
}

func makemap(t *rtype, cap int) (m unsafe.Pointer) {
	return unsafe.Pointer(js.Global.Get("Object").New().Unsafe())
}

func keyFor(t *rtype, key unsafe.Pointer) (*js.Object, string) {
	kv := js.InternalObject(key)
	if kv.Get("$get") != js.Undefined {
		kv = kv.Call("$get")
	}
	k := jsType(t.Key()).Call("keyFor", kv).String()
	return kv, k
}

func mapaccess(t *rtype, m, key unsafe.Pointer) unsafe.Pointer {
	_, k := keyFor(t, key)
	entry := js.InternalObject(m).Get(k)
	if entry == js.Undefined {
		return nil
	}
	return unsafe.Pointer(js.Global.Call("$newDataPointer", entry.Get("v"), jsType(PtrTo(t.Elem()))).Unsafe())
}

func mapassign(t *rtype, m, key, val unsafe.Pointer) {
	kv, k := keyFor(t, key)
	jsVal := js.InternalObject(val).Call("$get")
	et := t.Elem()
	if et.Kind() == Struct {
		newVal := jsType(et).Call("zero")
		copyStruct(newVal, jsVal, et)
		jsVal = newVal
	}
	entry := js.Global.Get("Object").New()
	entry.Set("k", kv)
	entry.Set("v", jsVal)
	js.InternalObject(m).Set(k, entry)
}

func mapdelete(t *rtype, m unsafe.Pointer, key unsafe.Pointer) {
	_, k := keyFor(t, key)
	js.InternalObject(m).Delete(k)
}

// TODO(nevkonatkte): The following three "faststr" implementations are meant to
// perform better for the common case of string-keyed maps (see upstream:
// https://github.com/golang/go/commit/23832ba2e2fb396cda1dacf3e8afcb38ec36dcba)
// However, the stubs below will perform the same or worse because of the extra
// string-to-pointer conversion. Not sure how to fix this without significant
// code duplication, however.

func mapaccess_faststr(t *rtype, m unsafe.Pointer, key string) (val unsafe.Pointer) {
	return mapaccess(t, m, unsafe.Pointer(&key))
}

func mapassign_faststr(t *rtype, m unsafe.Pointer, key string, val unsafe.Pointer) {
	mapassign(t, m, unsafe.Pointer(&key), val)
}

func mapdelete_faststr(t *rtype, m unsafe.Pointer, key string) {
	mapdelete(t, m, unsafe.Pointer(&key))
}

type hiter struct {
	t    Type
	m    *js.Object // Underlying map object.
	keys *js.Object
	i    int

	// last is the last object the iterator indicates. If this object exists, the
	// functions that return the current key or value returns this object,
	// regardless of the current iterator. It is because the current iterator
	// might be stale due to key deletion in a loop.
	last *js.Object
}

func (iter *hiter) skipUntilValidKey() {
	for iter.i < iter.keys.Length() {
		k := iter.keys.Index(iter.i)
		if iter.m.Get(k.String()) != js.Undefined {
			break
		}
		// The key is already deleted. Move on the next item.
		iter.i++
	}
}

func mapiterinit(t *rtype, m unsafe.Pointer, it *hiter) {
	*it = hiter{
		t:    t,
		m:    js.InternalObject(m),
		keys: js.Global.Call("$keys", js.InternalObject(m)),
		i:    0,
		last: nil,
	}
}

func mapiterkey(it *hiter) unsafe.Pointer {
	var kv *js.Object
	if it.last != nil {
		kv = it.last
	} else {
		it.skipUntilValidKey()
		if it.i == it.keys.Length() {
			return nil
		}
		k := it.keys.Index(it.i)
		kv = it.m.Get(k.String())

		// Record the key-value pair for later accesses.
		it.last = kv
	}
	return unsafe.Pointer(js.Global.Call("$newDataPointer", kv.Get("k"), jsType(PtrTo(it.t.Key()))).Unsafe())
}

func mapiterelem(it *hiter) unsafe.Pointer {
	var kv *js.Object
	if it.last != nil {
		kv = it.last
	} else {
		it.skipUntilValidKey()
		if it.i == it.keys.Length() {
			return nil
		}
		k := it.keys.Index(it.i)
		kv = it.m.Get(k.String())
		it.last = kv
	}
	return unsafe.Pointer(js.Global.Call("$newDataPointer", kv.Get("v"), jsType(PtrTo(it.t.Elem()))).Unsafe())
}

func mapiternext(it *hiter) {
	it.last = nil
	it.i++
}

func maplen(m unsafe.Pointer) int {
	return js.Global.Call("$keys", js.InternalObject(m)).Length()
}

func cvtDirect(v Value, typ Type) Value {
	srcVal := v.object()
	if srcVal == jsType(v.typ).Get("nil") {
		return makeValue(typ, jsType(typ).Get("nil"), v.flag)
	}

	var val *js.Object
	switch k := typ.Kind(); k {
	case Slice:
		slice := jsType(typ).New(srcVal.Get("$array"))
		slice.Set("$offset", srcVal.Get("$offset"))
		slice.Set("$length", srcVal.Get("$length"))
		slice.Set("$capacity", srcVal.Get("$capacity"))
		val = js.Global.Call("$newDataPointer", slice, jsType(PtrTo(typ)))
	case Ptr:
		switch typ.Elem().Kind() {
		case Struct:
			if typ.Elem() == v.typ.Elem() {
				val = srcVal
				break
			}
			val = jsType(typ).New()
			copyStruct(val, srcVal, typ.Elem())
		case Array:
			// Unlike other pointers, array pointers are "wrapped" types (see
			// isWrapped() in the compiler package), and are represented by a native
			// javascript array object here.
			val = srcVal
		default:
			val = jsType(typ).New(srcVal.Get("$get"), srcVal.Get("$set"))
		}
	case Struct:
		val = jsType(typ).Get("ptr").New()
		copyStruct(val, srcVal, typ)
	case Array, Bool, Chan, Func, Interface, Map, String, UnsafePointer:
		val = js.InternalObject(v.ptr)
	default:
		panic(&ValueError{"reflect.Convert", k})
	}
	return Value{typ.common(), unsafe.Pointer(val.Unsafe()), v.flag.ro() | v.flag&flagIndir | flag(typ.Kind())}
}

// convertOp: []T -> *[N]T
func cvtSliceArrayPtr(v Value, t Type) Value {
	slice := v.object()

	slen := slice.Get("$length").Int()
	alen := t.Elem().Len()
	if alen > slen {
		panic("reflect: cannot convert slice with length " + itoa.Itoa(slen) + " to pointer to array with length " + itoa.Itoa(alen))
	}
	array := js.Global.Call("$sliceToGoArray", slice, jsType(t))
	return Value{t.common(), unsafe.Pointer(array.Unsafe()), v.flag&^(flagIndir|flagAddr|flagKindMask) | flag(Ptr)}
}

func Copy(dst, src Value) int {
	dk := dst.kind()
	if dk != Array && dk != Slice {
		panic(&ValueError{"reflect.Copy", dk})
	}
	if dk == Array {
		dst.mustBeAssignable()
	}
	dst.mustBeExported()

	sk := src.kind()
	var stringCopy bool
	if sk != Array && sk != Slice {
		stringCopy = sk == String && dst.typ.Elem().Kind() == Uint8
		if !stringCopy {
			panic(&ValueError{"reflect.Copy", sk})
		}
	}
	src.mustBeExported()

	if !stringCopy {
		typesMustMatch("reflect.Copy", dst.typ.Elem(), src.typ.Elem())
	}

	dstVal := dst.object()
	if dk == Array {
		dstVal = jsType(SliceOf(dst.typ.Elem())).New(dstVal)
	}

	srcVal := src.object()
	if sk == Array {
		srcVal = jsType(SliceOf(src.typ.Elem())).New(srcVal)
	}

	if stringCopy {
		return js.Global.Call("$copyString", dstVal, srcVal).Int()
	}
	return js.Global.Call("$copySlice", dstVal, srcVal).Int()
}

func methodReceiver(op string, v Value, i int) (_ *rtype, t *funcType, fn unsafe.Pointer) {
	var prop string
	if v.typ.Kind() == Interface {
		tt := (*interfaceType)(unsafe.Pointer(v.typ))
		if i < 0 || i >= len(tt.methods) {
			panic("reflect: internal error: invalid method index")
		}
		m := &tt.methods[i]
		if !tt.nameOff(m.name).isExported() {
			panic("reflect: " + op + " of unexported method")
		}
		t = (*funcType)(unsafe.Pointer(tt.typeOff(m.typ)))
		prop = tt.nameOff(m.name).name()
	} else {
		ms := v.typ.exportedMethods()
		if uint(i) >= uint(len(ms)) {
			panic("reflect: internal error: invalid method index")
		}
		m := ms[i]
		if !v.typ.nameOff(m.name).isExported() {
			panic("reflect: " + op + " of unexported method")
		}
		t = (*funcType)(unsafe.Pointer(v.typ.typeOff(m.mtyp)))
		prop = js.Global.Call("$methodSet", jsType(v.typ)).Index(i).Get("prop").String()
	}
	rcvr := v.object()
	if isWrapped(v.typ) {
		rcvr = jsType(v.typ).New(rcvr)
	}
	fn = unsafe.Pointer(rcvr.Get(prop).Unsafe())
	return
}

func valueInterface(v Value, safe bool) interface{} {
	if v.flag == 0 {
		panic(&ValueError{"reflect.Value.Interface", 0})
	}
	if safe && v.flag&flagRO != 0 {
		panic("reflect.Value.Interface: cannot return value obtained from unexported field or method")
	}
	if v.flag&flagMethod != 0 {
		v = makeMethodValue("Interface", v)
	}

	if isWrapped(v.typ) {
		return interface{}(unsafe.Pointer(jsType(v.typ).New(v.object()).Unsafe()))
	}
	return interface{}(unsafe.Pointer(v.object().Unsafe()))
}

func ifaceE2I(t *rtype, src interface{}, dst unsafe.Pointer) {
	js.InternalObject(dst).Call("$set", js.InternalObject(src))
}

func makeMethodValue(op string, v Value) Value {
	if v.flag&flagMethod == 0 {
		panic("reflect: internal error: invalid use of makePartialFunc")
	}

	_, _, fn := methodReceiver(op, v, int(v.flag)>>flagMethodShift)
	rcvr := v.object()
	if isWrapped(v.typ) {
		rcvr = jsType(v.typ).New(rcvr)
	}
	fv := js.MakeFunc(func(this *js.Object, arguments []*js.Object) interface{} {
		return js.InternalObject(fn).Call("apply", rcvr, arguments)
	})
	return Value{v.Type().common(), unsafe.Pointer(fv.Unsafe()), v.flag.ro() | flag(Func)}
}

func (t *rtype) pointers() bool {
	switch t.Kind() {
	case Ptr, Map, Chan, Func, Struct, Array:
		return true
	default:
		return false
	}
}

func (t *rtype) Comparable() bool {
	switch t.Kind() {
	case Func, Slice, Map:
		return false
	case Array:
		return t.Elem().Comparable()
	case Struct:
		for i := 0; i < t.NumField(); i++ {
			if !t.Field(i).Type.Comparable() {
				return false
			}
		}
	}
	return true
}

func (t *rtype) Method(i int) (m Method) {
	if t.Kind() == Interface {
		tt := (*interfaceType)(unsafe.Pointer(t))
		return tt.Method(i)
	}
	methods := t.exportedMethods()
	if i < 0 || i >= len(methods) {
		panic("reflect: Method index out of range")
	}
	p := methods[i]
	pname := t.nameOff(p.name)
	m.Name = pname.name()
	fl := flag(Func)
	mtyp := t.typeOff(p.mtyp)
	ft := (*funcType)(unsafe.Pointer(mtyp))
	in := make([]Type, 0, 1+len(ft.in()))
	in = append(in, t)
	for _, arg := range ft.in() {
		in = append(in, arg)
	}
	out := make([]Type, 0, len(ft.out()))
	for _, ret := range ft.out() {
		out = append(out, ret)
	}
	mt := FuncOf(in, out, ft.IsVariadic())
	m.Type = mt
	prop := js.Global.Call("$methodSet", js.InternalObject(t).Get("jsType")).Index(i).Get("prop").String()
	fn := js.MakeFunc(func(this *js.Object, arguments []*js.Object) interface{} {
		rcvr := arguments[0]
		return rcvr.Get(prop).Call("apply", rcvr, arguments[1:])
	})
	m.Func = Value{mt.(*rtype), unsafe.Pointer(fn.Unsafe()), fl}

	m.Index = i
	return m
}

func (v Value) object() *js.Object {
	if v.typ.Kind() == Array || v.typ.Kind() == Struct {
		return js.InternalObject(v.ptr)
	}
	if v.flag&flagIndir != 0 {
		val := js.InternalObject(v.ptr).Call("$get")
		if val != js.Global.Get("$ifaceNil") && val.Get("constructor") != jsType(v.typ) {
			switch v.typ.Kind() {
			case Uint64, Int64:
				val = jsType(v.typ).New(val.Get("$high"), val.Get("$low"))
			case Complex64, Complex128:
				val = jsType(v.typ).New(val.Get("$real"), val.Get("$imag"))
			case Slice:
				if val == val.Get("constructor").Get("nil") {
					val = jsType(v.typ).Get("nil")
					break
				}
				newVal := jsType(v.typ).New(val.Get("$array"))
				newVal.Set("$offset", val.Get("$offset"))
				newVal.Set("$length", val.Get("$length"))
				newVal.Set("$capacity", val.Get("$capacity"))
				val = newVal
			}
		}
		return js.InternalObject(val.Unsafe())
	}
	return js.InternalObject(v.ptr)
}

func (v Value) assignTo(context string, dst *rtype, target unsafe.Pointer) Value {
	if v.flag&flagMethod != 0 {
		v = makeMethodValue(context, v)
	}

	switch {
	case directlyAssignable(dst, v.typ):
		// Overwrite type so that they match.
		// Same memory layout, so no harm done.
		fl := v.flag&(flagAddr|flagIndir) | v.flag.ro()
		fl |= flag(dst.Kind())
		return Value{dst, v.ptr, fl}

	case implements(dst, v.typ):
		if target == nil {
			target = unsafe_New(dst)
		}
		// GopherJS: Skip the v.Kind() == Interface && v.IsNil() if statement
		//           from upstream. ifaceE2I below does not panic, and it needs
		//           to run, given its custom implementation.
		x := valueInterface(v, false)
		if dst.NumMethod() == 0 {
			*(*interface{})(target) = x
		} else {
			ifaceE2I(dst, x, target)
		}
		return Value{dst, target, flagIndir | flag(Interface)}
	}

	// Failed.
	panic(context + ": value of type " + v.typ.String() + " is not assignable to type " + dst.String())
}

var callHelper = js.Global.Get("$call").Interface().(func(...interface{}) *js.Object)

func (v Value) call(op string, in []Value) []Value {
	var (
		t    *funcType
		fn   unsafe.Pointer
		rcvr *js.Object
	)
	if v.flag&flagMethod != 0 {
		_, t, fn = methodReceiver(op, v, int(v.flag)>>flagMethodShift)
		rcvr = v.object()
		if isWrapped(v.typ) {
			rcvr = jsType(v.typ).New(rcvr)
		}
	} else {
		t = (*funcType)(unsafe.Pointer(v.typ))
		fn = unsafe.Pointer(v.object().Unsafe())
		rcvr = js.Undefined
	}

	if fn == nil {
		panic("reflect.Value.Call: call of nil function")
	}

	isSlice := op == "CallSlice"
	n := t.NumIn()
	if isSlice {
		if !t.IsVariadic() {
			panic("reflect: CallSlice of non-variadic function")
		}
		if len(in) < n {
			panic("reflect: CallSlice with too few input arguments")
		}
		if len(in) > n {
			panic("reflect: CallSlice with too many input arguments")
		}
	} else {
		if t.IsVariadic() {
			n--
		}
		if len(in) < n {
			panic("reflect: Call with too few input arguments")
		}
		if !t.IsVariadic() && len(in) > n {
			panic("reflect: Call with too many input arguments")
		}
	}
	for _, x := range in {
		if x.Kind() == Invalid {
			panic("reflect: " + op + " using zero Value argument")
		}
	}
	for i := 0; i < n; i++ {
		if xt, targ := in[i].Type(), t.In(i); !xt.AssignableTo(targ) {
			panic("reflect: " + op + " using " + xt.String() + " as type " + targ.String())
		}
	}
	if !isSlice && t.IsVariadic() {
		// prepare slice for remaining values
		m := len(in) - n
		slice := MakeSlice(t.In(n), m, m)
		elem := t.In(n).Elem()
		for i := 0; i < m; i++ {
			x := in[n+i]
			if xt := x.Type(); !xt.AssignableTo(elem) {
				panic("reflect: cannot use " + xt.String() + " as type " + elem.String() + " in " + op)
			}
			slice.Index(i).Set(x)
		}
		origIn := in
		in = make([]Value, n+1)
		copy(in[:n], origIn)
		in[n] = slice
	}

	nin := len(in)
	if nin != t.NumIn() {
		panic("reflect.Value.Call: wrong argument count")
	}
	nout := t.NumOut()

	argsArray := js.Global.Get("Array").New(t.NumIn())
	for i, arg := range in {
		argsArray.SetIndex(i, unwrapJsObject(t.In(i), arg.assignTo("reflect.Value.Call", t.In(i).common(), nil).object()))
	}
	results := callHelper(js.InternalObject(fn), rcvr, argsArray)

	switch nout {
	case 0:
		return nil
	case 1:
		return []Value{makeValue(t.Out(0), wrapJsObject(t.Out(0), results), 0)}
	default:
		ret := make([]Value, nout)
		for i := range ret {
			ret[i] = makeValue(t.Out(i), wrapJsObject(t.Out(i), results.Index(i)), 0)
		}
		return ret
	}
}

func (v Value) Cap() int {
	k := v.kind()
	switch k {
	case Array:
		return v.typ.Len()
	case Chan, Slice:
		return v.object().Get("$capacity").Int()
	}
	panic(&ValueError{"reflect.Value.Cap", k})
}

var jsObjectPtr = reflectType(js.Global.Get("$jsObjectPtr"))

func wrapJsObject(typ Type, val *js.Object) *js.Object {
	if typ == jsObjectPtr {
		return jsType(jsObjectPtr).New(val)
	}
	return val
}

func unwrapJsObject(typ Type, val *js.Object) *js.Object {
	if typ == jsObjectPtr {
		return val.Get("object")
	}
	return val
}

func (v Value) Elem() Value {
	switch k := v.kind(); k {
	case Interface:
		val := v.object()
		if val == js.Global.Get("$ifaceNil") {
			return Value{}
		}
		typ := reflectType(val.Get("constructor"))
		return makeValue(typ, val.Get("$val"), v.flag.ro())

	case Ptr:
		if v.IsNil() {
			return Value{}
		}
		val := v.object()
		tt := (*ptrType)(unsafe.Pointer(v.typ))
		fl := v.flag&flagRO | flagIndir | flagAddr
		fl |= flag(tt.elem.Kind())
		return Value{tt.elem, unsafe.Pointer(wrapJsObject(tt.elem, val).Unsafe()), fl}

	default:
		panic(&ValueError{"reflect.Value.Elem", k})
	}
}

func (v Value) Field(i int) Value {
	if v.kind() != Struct {
		panic(&ValueError{"reflect.Value.Field", v.kind()})
	}
	tt := (*structType)(unsafe.Pointer(v.typ))
	if uint(i) >= uint(len(tt.fields)) {
		panic("reflect: Field index out of range")
	}

	prop := jsType(v.typ).Get("fields").Index(i).Get("prop").String()
	field := &tt.fields[i]
	typ := field.typ

	fl := v.flag&(flagStickyRO|flagIndir|flagAddr) | flag(typ.Kind())
	if !field.name.isExported() {
		if field.embedded() {
			fl |= flagEmbedRO
		} else {
			fl |= flagStickyRO
		}
	}

	if tag := tt.fields[i].name.tag(); tag != "" && i != 0 {
		if jsTag := getJsTag(tag); jsTag != "" {
			for {
				v = v.Field(0)
				if v.typ == jsObjectPtr {
					o := v.object().Get("object")
					return Value{typ, unsafe.Pointer(jsType(PtrTo(typ)).New(
						js.InternalObject(func() *js.Object { return js.Global.Call("$internalize", o.Get(jsTag), jsType(typ)) }),
						js.InternalObject(func(x *js.Object) { o.Set(jsTag, js.Global.Call("$externalize", x, jsType(typ))) }),
					).Unsafe()), fl}
				}
				if v.typ.Kind() == Ptr {
					v = v.Elem()
				}
			}
		}
	}

	s := js.InternalObject(v.ptr)
	if fl&flagIndir != 0 && typ.Kind() != Array && typ.Kind() != Struct {
		return Value{typ, unsafe.Pointer(jsType(PtrTo(typ)).New(
			js.InternalObject(func() *js.Object { return wrapJsObject(typ, s.Get(prop)) }),
			js.InternalObject(func(x *js.Object) { s.Set(prop, unwrapJsObject(typ, x)) }),
		).Unsafe()), fl}
	}
	return makeValue(typ, wrapJsObject(typ, s.Get(prop)), fl)
}

func getJsTag(tag string) string {
	for tag != "" {
		// skip leading space
		i := 0
		for i < len(tag) && tag[i] == ' ' {
			i++
		}
		tag = tag[i:]
		if tag == "" {
			break
		}

		// scan to colon.
		// a space or a quote is a syntax error
		i = 0
		for i < len(tag) && tag[i] != ' ' && tag[i] != ':' && tag[i] != '"' {
			i++
		}
		if i+1 >= len(tag) || tag[i] != ':' || tag[i+1] != '"' {
			break
		}
		name := string(tag[:i])
		tag = tag[i+1:]

		// scan quoted string to find value
		i = 1
		for i < len(tag) && tag[i] != '"' {
			if tag[i] == '\\' {
				i++
			}
			i++
		}
		if i >= len(tag) {
			break
		}
		qvalue := string(tag[:i+1])
		tag = tag[i+1:]

		if name == "js" {
			value, _ := strconv.Unquote(qvalue)
			return value
		}
	}
	return ""
}

func (v Value) Index(i int) Value {
	switch k := v.kind(); k {
	case Array:
		tt := (*arrayType)(unsafe.Pointer(v.typ))
		if i < 0 || i > int(tt.len) {
			panic("reflect: array index out of range")
		}
		typ := tt.elem
		fl := v.flag&(flagIndir|flagAddr) | v.flag.ro() | flag(typ.Kind())

		a := js.InternalObject(v.ptr)
		if fl&flagIndir != 0 && typ.Kind() != Array && typ.Kind() != Struct {
			return Value{typ, unsafe.Pointer(jsType(PtrTo(typ)).New(
				js.InternalObject(func() *js.Object { return wrapJsObject(typ, a.Index(i)) }),
				js.InternalObject(func(x *js.Object) { a.SetIndex(i, unwrapJsObject(typ, x)) }),
			).Unsafe()), fl}
		}
		return makeValue(typ, wrapJsObject(typ, a.Index(i)), fl)

	case Slice:
		s := v.object()
		if i < 0 || i >= s.Get("$length").Int() {
			panic("reflect: slice index out of range")
		}
		tt := (*sliceType)(unsafe.Pointer(v.typ))
		typ := tt.elem
		fl := flagAddr | flagIndir | v.flag.ro() | flag(typ.Kind())

		i += s.Get("$offset").Int()
		a := s.Get("$array")
		if fl&flagIndir != 0 && typ.Kind() != Array && typ.Kind() != Struct {
			return Value{typ, unsafe.Pointer(jsType(PtrTo(typ)).New(
				js.InternalObject(func() *js.Object { return wrapJsObject(typ, a.Index(i)) }),
				js.InternalObject(func(x *js.Object) { a.SetIndex(i, unwrapJsObject(typ, x)) }),
			).Unsafe()), fl}
		}
		return makeValue(typ, wrapJsObject(typ, a.Index(i)), fl)

	case String:
		str := *(*string)(v.ptr)
		if i < 0 || i >= len(str) {
			panic("reflect: string index out of range")
		}
		fl := v.flag.ro() | flag(Uint8) | flagIndir
		c := str[i]
		return Value{uint8Type, unsafe.Pointer(&c), fl}

	default:
		panic(&ValueError{"reflect.Value.Index", k})
	}
}

func (v Value) InterfaceData() [2]uintptr {
	panic(errors.New("InterfaceData is not supported by GopherJS"))
}

func (v Value) IsNil() bool {
	switch k := v.kind(); k {
	case Ptr, Slice:
		return v.object() == jsType(v.typ).Get("nil")
	case Chan:
		return v.object() == js.Global.Get("$chanNil")
	case Func:
		return v.object() == js.Global.Get("$throwNilPointerError")
	case Map:
		return v.object() == js.InternalObject(false)
	case Interface:
		return v.object() == js.Global.Get("$ifaceNil")
	case UnsafePointer:
		return v.object().Unsafe() == 0
	default:
		panic(&ValueError{"reflect.Value.IsNil", k})
	}
}

func (v Value) Len() int {
	switch k := v.kind(); k {
	case Array, String:
		return v.object().Length()
	case Slice:
		return v.object().Get("$length").Int()
	case Chan:
		return v.object().Get("$buffer").Get("length").Int()
	case Map:
		return js.Global.Call("$keys", v.object()).Length()
	default:
		panic(&ValueError{"reflect.Value.Len", k})
	}
}

func (v Value) Pointer() uintptr {
	switch k := v.kind(); k {
	case Chan, Map, Ptr, UnsafePointer:
		if v.IsNil() {
			return 0
		}
		return v.object().Unsafe()
	case Func:
		if v.IsNil() {
			return 0
		}
		return 1
	case Slice:
		if v.IsNil() {
			return 0
		}
		return v.object().Get("$array").Unsafe()
	default:
		panic(&ValueError{"reflect.Value.Pointer", k})
	}
}

func (v Value) Set(x Value) {
	v.mustBeAssignable()
	x.mustBeExported()
	x = x.assignTo("reflect.Set", v.typ, nil)
	if v.flag&flagIndir != 0 {
		switch v.typ.Kind() {
		case Array:
			jsType(v.typ).Call("copy", js.InternalObject(v.ptr), js.InternalObject(x.ptr))
		case Interface:
			js.InternalObject(v.ptr).Call("$set", js.InternalObject(valueInterface(x, false)))
		case Struct:
			copyStruct(js.InternalObject(v.ptr), js.InternalObject(x.ptr), v.typ)
		default:
			js.InternalObject(v.ptr).Call("$set", x.object())
		}
		return
	}
	v.ptr = x.ptr
}

func (v Value) SetBytes(x []byte) {
	v.mustBeAssignable()
	v.mustBe(Slice)
	if v.typ.Elem().Kind() != Uint8 {
		panic("reflect.Value.SetBytes of non-byte slice")
	}
	slice := js.InternalObject(x)
	if v.typ.Name() != "" || v.typ.Elem().Name() != "" {
		typedSlice := jsType(v.typ).New(slice.Get("$array"))
		typedSlice.Set("$offset", slice.Get("$offset"))
		typedSlice.Set("$length", slice.Get("$length"))
		typedSlice.Set("$capacity", slice.Get("$capacity"))
		slice = typedSlice
	}
	js.InternalObject(v.ptr).Call("$set", slice)
}

func (v Value) SetCap(n int) {
	v.mustBeAssignable()
	v.mustBe(Slice)
	s := js.InternalObject(v.ptr).Call("$get")
	if n < s.Get("$length").Int() || n > s.Get("$capacity").Int() {
		panic("reflect: slice capacity out of range in SetCap")
	}
	newSlice := jsType(v.typ).New(s.Get("$array"))
	newSlice.Set("$offset", s.Get("$offset"))
	newSlice.Set("$length", s.Get("$length"))
	newSlice.Set("$capacity", n)
	js.InternalObject(v.ptr).Call("$set", newSlice)
}

func (v Value) SetLen(n int) {
	v.mustBeAssignable()
	v.mustBe(Slice)
	s := js.InternalObject(v.ptr).Call("$get")
	if n < 0 || n > s.Get("$capacity").Int() {
		panic("reflect: slice length out of range in SetLen")
	}
	newSlice := jsType(v.typ).New(s.Get("$array"))
	newSlice.Set("$offset", s.Get("$offset"))
	newSlice.Set("$length", n)
	newSlice.Set("$capacity", s.Get("$capacity"))
	js.InternalObject(v.ptr).Call("$set", newSlice)
}

func (v Value) Slice(i, j int) Value {
	var (
		cap int
		typ Type
		s   *js.Object
	)
	switch kind := v.kind(); kind {
	case Array:
		if v.flag&flagAddr == 0 {
			panic("reflect.Value.Slice: slice of unaddressable array")
		}
		tt := (*arrayType)(unsafe.Pointer(v.typ))
		cap = int(tt.len)
		typ = SliceOf(tt.elem)
		s = jsType(typ).New(v.object())

	case Slice:
		typ = v.typ
		s = v.object()
		cap = s.Get("$capacity").Int()

	case String:
		str := *(*string)(v.ptr)
		if i < 0 || j < i || j > len(str) {
			panic("reflect.Value.Slice: string slice index out of bounds")
		}
		return ValueOf(str[i:j])

	default:
		panic(&ValueError{"reflect.Value.Slice", kind})
	}

	if i < 0 || j < i || j > cap {
		panic("reflect.Value.Slice: slice index out of bounds")
	}

	return makeValue(typ, js.Global.Call("$subslice", s, i, j), v.flag.ro())
}

func (v Value) Slice3(i, j, k int) Value {
	var (
		cap int
		typ Type
		s   *js.Object
	)
	switch kind := v.kind(); kind {
	case Array:
		if v.flag&flagAddr == 0 {
			panic("reflect.Value.Slice: slice of unaddressable array")
		}
		tt := (*arrayType)(unsafe.Pointer(v.typ))
		cap = int(tt.len)
		typ = SliceOf(tt.elem)
		s = jsType(typ).New(v.object())

	case Slice:
		typ = v.typ
		s = v.object()
		cap = s.Get("$capacity").Int()

	default:
		panic(&ValueError{"reflect.Value.Slice3", kind})
	}

	if i < 0 || j < i || k < j || k > cap {
		panic("reflect.Value.Slice3: slice index out of bounds")
	}

	return makeValue(typ, js.Global.Call("$subslice", s, i, j, k), v.flag.ro())
}

func (v Value) Close() {
	v.mustBe(Chan)
	v.mustBeExported()
	js.Global.Call("$close", v.object())
}

var selectHelper = js.Global.Get("$select").Interface().(func(...interface{}) *js.Object)

func chanrecv(ch unsafe.Pointer, nb bool, val unsafe.Pointer) (selected, received bool) {
	comms := [][]*js.Object{{js.InternalObject(ch)}}
	if nb {
		comms = append(comms, []*js.Object{})
	}
	selectRes := selectHelper(comms)
	if nb && selectRes.Index(0).Int() == 1 {
		return false, false
	}
	recvRes := selectRes.Index(1)
	js.InternalObject(val).Call("$set", recvRes.Index(0))
	return true, recvRes.Index(1).Bool()
}

func chansend(ch unsafe.Pointer, val unsafe.Pointer, nb bool) bool {
	comms := [][]*js.Object{{js.InternalObject(ch), js.InternalObject(val).Call("$get")}}
	if nb {
		comms = append(comms, []*js.Object{})
	}
	selectRes := selectHelper(comms)
	if nb && selectRes.Index(0).Int() == 1 {
		return false
	}
	return true
}

func rselect(rselects []runtimeSelect) (chosen int, recvOK bool) {
	comms := make([][]*js.Object, len(rselects))
	for i, s := range rselects {
		switch SelectDir(s.dir) {
		case SelectDefault:
			comms[i] = []*js.Object{}
		case SelectRecv:
			ch := js.Global.Get("$chanNil")
			if js.InternalObject(s.ch) != js.InternalObject(0) {
				ch = js.InternalObject(s.ch)
			}
			comms[i] = []*js.Object{ch}
		case SelectSend:
			ch := js.Global.Get("$chanNil")
			var val *js.Object
			if js.InternalObject(s.ch) != js.InternalObject(0) {
				ch = js.InternalObject(s.ch)
				val = js.InternalObject(s.val).Call("$get")
			}
			comms[i] = []*js.Object{ch, val}
		}
	}
	selectRes := selectHelper(comms)
	c := selectRes.Index(0).Int()
	if SelectDir(rselects[c].dir) == SelectRecv {
		recvRes := selectRes.Index(1)
		js.InternalObject(rselects[c].val).Call("$set", recvRes.Index(0))
		return c, recvRes.Index(1).Bool()
	}
	return c, false
}

func DeepEqual(a1, a2 interface{}) bool {
	i1 := js.InternalObject(a1)
	i2 := js.InternalObject(a2)
	if i1 == i2 {
		return true
	}
	if i1 == nil || i2 == nil || i1.Get("constructor") != i2.Get("constructor") {
		return false
	}
	return deepValueEqualJs(ValueOf(a1), ValueOf(a2), nil)
}

func deepValueEqualJs(v1, v2 Value, visited [][2]unsafe.Pointer) bool {
	if !v1.IsValid() || !v2.IsValid() {
		return !v1.IsValid() && !v2.IsValid()
	}
	if v1.Type() != v2.Type() {
		return false
	}
	if v1.Type() == jsObjectPtr {
		return unwrapJsObject(jsObjectPtr, v1.object()) == unwrapJsObject(jsObjectPtr, v2.object())
	}

	switch v1.Kind() {
	case Array, Map, Slice, Struct:
		for _, entry := range visited {
			if v1.ptr == entry[0] && v2.ptr == entry[1] {
				return true
			}
		}
		visited = append(visited, [2]unsafe.Pointer{v1.ptr, v2.ptr})
	}

	switch v1.Kind() {
	case Array, Slice:
		if v1.Kind() == Slice {
			if v1.IsNil() != v2.IsNil() {
				return false
			}
			if v1.object() == v2.object() {
				return true
			}
		}
		n := v1.Len()
		if n != v2.Len() {
			return false
		}
		for i := 0; i < n; i++ {
			if !deepValueEqualJs(v1.Index(i), v2.Index(i), visited) {
				return false
			}
		}
		return true
	case Interface:
		if v1.IsNil() || v2.IsNil() {
			return v1.IsNil() && v2.IsNil()
		}
		return deepValueEqualJs(v1.Elem(), v2.Elem(), visited)
	case Ptr:
		return deepValueEqualJs(v1.Elem(), v2.Elem(), visited)
	case Struct:
		n := v1.NumField()
		for i := 0; i < n; i++ {
			if !deepValueEqualJs(v1.Field(i), v2.Field(i), visited) {
				return false
			}
		}
		return true
	case Map:
		if v1.IsNil() != v2.IsNil() {
			return false
		}
		if v1.object() == v2.object() {
			return true
		}
		keys := v1.MapKeys()
		if len(keys) != v2.Len() {
			return false
		}
		for _, k := range keys {
			val1 := v1.MapIndex(k)
			val2 := v2.MapIndex(k)
			if !val1.IsValid() || !val2.IsValid() || !deepValueEqualJs(val1, val2, visited) {
				return false
			}
		}
		return true
	case Func:
		return v1.IsNil() && v2.IsNil()
	case UnsafePointer:
		return v1.object() == v2.object()
	}

	return js.Global.Call("$interfaceIsEqual", js.InternalObject(valueInterface(v1, false)), js.InternalObject(valueInterface(v2, false))).Bool()
}

func methodNameSkip() string {
	pc, _, _, _ := runtime.Caller(3)
	f := runtime.FuncForPC(pc)
	if f == nil {
		return "unknown method"
	}
	// Function name extracted from the call stack can be different from vanilla
	// Go. Here we try to fix stuff like "Object.$packages.reflect.Q.ptr.SetIterKey"
	// into "Value.SetIterKey".
	// This workaround may become obsolete after https://github.com/gopherjs/gopherjs/issues/1085
	// is resolved.
	name := f.Name()
	idx := len(name) - 1
	for idx > 0 {
		if name[idx] == '.' {
			break
		}
		idx--
	}
	if idx < 0 {
		return name
	}
	return "Value" + name[idx:]
}

func verifyNotInHeapPtr(p uintptr) bool {
	// Go runtime uses this method to make sure that a uintptr won't crash GC if
	// interpreted as a heap pointer. This is not relevant for GopherJS, so we can
	// always return true.
	return true
}
