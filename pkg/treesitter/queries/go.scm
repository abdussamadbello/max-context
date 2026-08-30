; Package clause — the file's own package, for same-package resolution.
(package_clause (package_identifier) @package)

; Package-level constants/vars: top-level `const Name = …` / `var Name = …`.
; Anchored at source_file scope so locals inside functions are excluded.
(source_file
  (const_declaration
    (const_spec name: (identifier) @const.name)))
(source_file
  (var_declaration
    (var_spec name: (identifier) @const.name)))

; Functions: name + body. @func.body presence marks a definition.
; Optional single-value return type (9a) arrives in the same group.
(function_declaration
  name: (identifier) @func.name
  result: [
    (type_identifier) @func.result.type
    (pointer_type (type_identifier) @func.result.type)
  ]?
  body: (block) @func.body)

; Methods: capture the receiver name + type so methods are type-qualified.
;   func (f *Flags) Register(...) -> recv.name=f, recv.type=Flags, method.name=Register
(method_declaration
  receiver: (parameter_list
    (parameter_declaration
      name: (identifier)? @method.recv.name
      type: [
        (pointer_type (type_identifier) @method.recv.type)
        (pointer_type (qualified_type name: (type_identifier) @method.recv.type))
        (type_identifier) @method.recv.type
        (qualified_type name: (type_identifier) @method.recv.type)
      ]))
  name: (field_identifier) @method.name
  result: [
    (type_identifier) @func.result.type
    (pointer_type (type_identifier) @func.result.type)
  ]?
  body: (block) @func.body)

; Bare calls: same-package / builtin candidates.
(call_expression
  function: (identifier) @call.callee)

; Selector calls: KEEP the receiver operand AND the callee field.
;   db.Query() -> call.recv=db, call.callee=Query
(call_expression
  function: (selector_expression
    operand: (identifier) @call.recv
    field: (field_identifier) @call.callee))

; Function parameters with a declared type — receiver typing for method calls
; whose receiver is a parameter. Type normalized to its bare identifier.
(function_declaration
  name: (identifier) @sig.func.name
  parameters: (parameter_list
    (parameter_declaration
      name: (identifier) @param.name
      type: [
        (pointer_type (type_identifier) @param.type)
        (pointer_type (qualified_type name: (type_identifier) @param.type))
        (type_identifier) @param.type
        (qualified_type name: (type_identifier) @param.type)
      ])))

; Method parameters with a declared type (receiver handled separately above).
(method_declaration
  parameters: (parameter_list
    (parameter_declaration
      name: (identifier) @param.name
      type: [
        (pointer_type (type_identifier) @param.type)
        (pointer_type (qualified_type name: (type_identifier) @param.type))
        (type_identifier) @param.type
        (qualified_type name: (type_identifier) @param.type)
      ])))

; Statically-typed locals: x := T{} / x := &T{} / x := pkg.T{}
(short_var_declaration
  left: (expression_list (identifier) @local.name)
  right: (expression_list [
    (composite_literal type: (type_identifier) @local.type)
    (composite_literal type: (qualified_type name: (type_identifier) @local.type))
    (unary_expression operand: (composite_literal type: (type_identifier) @local.type))
    (unary_expression operand: (composite_literal type: (qualified_type name: (type_identifier) @local.type)))
  ]))

; Statically-typed locals: var x T / var x *T
(var_declaration
  (var_spec
    name: (identifier) @local.name
    type: [
      (type_identifier) @local.type
      (pointer_type (type_identifier) @local.type)
      (qualified_type name: (type_identifier) @local.type)
      (pointer_type (qualified_type name: (type_identifier) @local.type))
    ]))

; Element-typed collections: parameters and locals whose type is a slice, array,
; or map of a named type — `ns []Notifier`, `m map[string]Notifier`. Captured
; separately from param.type/local.type because the identifier's own type is the
; COLLECTION; what a range or index binding needs is the ELEMENT type. Without
; this, `for _, n := range ns` cannot type n at all, so an interface-typed
; element never reaches the dispatch fan-out.
(function_declaration
  parameters: (parameter_list
    (parameter_declaration
      name: (identifier) @elem.name
      type: [
        (slice_type element: (type_identifier) @elem.type)
        (slice_type element: (pointer_type (type_identifier) @elem.type))
        (array_type element: (type_identifier) @elem.type)
        (map_type value: (type_identifier) @elem.type)
        (map_type value: (pointer_type (type_identifier) @elem.type))
      ])))

(method_declaration
  parameters: (parameter_list
    (parameter_declaration
      name: (identifier) @elem.name
      type: [
        (slice_type element: (type_identifier) @elem.type)
        (slice_type element: (pointer_type (type_identifier) @elem.type))
        (array_type element: (type_identifier) @elem.type)
        (map_type value: (type_identifier) @elem.type)
        (map_type value: (pointer_type (type_identifier) @elem.type))
      ])))

(var_declaration
  (var_spec
    name: (identifier) @elem.name
    type: [
      (slice_type element: (type_identifier) @elem.type)
      (slice_type element: (pointer_type (type_identifier) @elem.type))
      (array_type element: (type_identifier) @elem.type)
      (map_type value: (type_identifier) @elem.type)
      (map_type value: (pointer_type (type_identifier) @elem.type))
    ]))

; Range bindings: `for _, n := range ns` and `for n := range ns`. Captures the
; bound identifier and the ranged collection; the parser types the binding from
; the collection's recorded element type.
(range_clause
  left: (expression_list (identifier) (identifier) @range.name)
  right: (identifier) @range.src)

(range_clause
  left: (expression_list (identifier) @range.name)
  right: (identifier) @range.src)

; Index bindings: `n := ns[0]`. Same element-type lookup as a range binding.
(short_var_declaration
  left: (expression_list (identifier) @range.name)
  right: (expression_list
    (index_expression operand: (identifier) @range.src)))

; Return-typed locals (9a): x := someCall(...)  — record the callee so the
; resolver can type x from that function's return_type. Bare call only; selector
; calls (pkg.New(), a.B()) are handled separately / left for a later cut.
(short_var_declaration
  left: (expression_list (identifier) @localcall.name)
  right: (expression_list
    (call_expression
      function: (identifier) @localcall.callee)))

; Struct field types (9b): for `type S struct { db *Conn }` -> (S, db, Conn).
(type_declaration
  (type_spec
    name: (type_identifier) @struct.name
    type: (struct_type
      (field_declaration_list
        (field_declaration
          name: (field_identifier) @field.name
          type: [
            (type_identifier) @field.type
            (pointer_type (type_identifier) @field.type)
            (qualified_type name: (type_identifier) @field.type)
            (pointer_type (qualified_type name: (type_identifier) @field.type))
          ])))))

; Selector calls with a field/selector receiver (9b): s.db.Query()
;   recvbase=s, recvfield=db, callee=Query
(call_expression
  function: (selector_expression
    operand: (selector_expression
      operand: (identifier) @callfield.base
      field: (field_identifier) @callfield.recv)
    field: (field_identifier) @callfield.callee))

; Types: name + definition.
(type_declaration
  (type_spec
    name: (type_identifier) @type.name
    type: (_) @type.def))

; Imports: alias (optional) + path. import_spec may sit directly under the
; declaration (single import) or inside an import_spec_list (grouped imports).
(import_spec
  name: (package_identifier)? @import.alias
  path: (interpreted_string_literal) @import.path)
