; ---- TypeScript (shared by tsx) ----
; Dotted capture names feed the dedicated parseTS path.

; Module-level constants/vars: top-level `const/let NAME = …` and
; `export const NAME = …`. Anchored at program/export scope so locals inside
; functions are excluded; makes config symbols searchable.
(program
  (lexical_declaration
    (variable_declarator name: (identifier) @const.name)))
(program
  (export_statement
    declaration: (lexical_declaration
      (variable_declarator name: (identifier) @const.name))))

; Classes: name + body range, so methods inside can resolve `this`.
(class_declaration
  name: (type_identifier) @class.name
  body: (class_body) @class.body)

; Functions: name + body (+ optional single return-type annotation).
(function_declaration
  name: (identifier) @func.name
  return_type: (type_annotation [
    (type_identifier) @func.result.type
    (predefined_type) @func.result.type
  ])?
  body: (statement_block) @func.body)

; Arrow functions assigned to a const/let: const foo = (...) => { ... }
(lexical_declaration
  (variable_declarator
    name: (identifier) @func.name
    value: (arrow_function
      body: (_) @func.body)))

; Methods: name + body. The enclosing class (by line range) gives `this`'s type.
(method_definition
  name: (property_identifier) @method.name
  return_type: (type_annotation [
    (type_identifier) @func.result.type
    (predefined_type) @func.result.type
  ])?
  body: (statement_block) @func.body)

; Class field types: `private db: Conn` -> (class, db, Conn) via struct_fields.
(public_field_definition
  name: (property_identifier) @field.name
  type: (type_annotation (type_identifier) @field.type))

; Typed parameters: (s: Server) -> param s typed Server.
(required_parameter
  pattern: (identifier) @param.name
  type: (type_annotation (type_identifier) @param.type))

; Element-typed collections: `ns: Notifier[]`, `ns: Array<Notifier>`. The
; identifier's own type is the collection; a `for..of` binding over it needs the
; ELEMENT type, which is why it is captured separately. Without this, an
; interface held in an array is never reached — the same gap the Go fixture
; exposed for `ns []Notifier`.
(required_parameter
  pattern: (identifier) @elem.name
  type: (type_annotation [
    (array_type (type_identifier) @elem.type)
    (generic_type
      name: (type_identifier) @_gen
      type_arguments: (type_arguments (type_identifier) @elem.type))
  ]))

; for (const n of ns) — bind n to ns's element type.
(for_in_statement
  left: (identifier) @range.name
  right: (identifier) @range.src)

; Typed locals: const a: Foo = ...
(variable_declarator
  name: (identifier) @local.name
  type: (type_annotation (type_identifier) @local.type))

; `new` locals: let b = new Engine();  -> b typed Engine.
(variable_declarator
  name: (identifier) @localnew.name
  value: (new_expression constructor: (identifier) @localnew.type))

; Return-typed locals: const x = make();  -> x typed from make()'s return.
(variable_declarator
  name: (identifier) @localcall.name
  value: (call_expression function: (identifier) @localcall.callee))

; Bare calls: foo()
(call_expression
  function: (identifier) @call.callee)

; Member calls: x.m()  -> recv=x, callee=m
(call_expression
  function: (member_expression
    object: (identifier) @call.recv
    property: (property_identifier) @call.callee))

; this.field.m()  -> field receiver: base=this, field, callee=m
(call_expression
  function: (member_expression
    object: (member_expression
      object: (this)
      property: (property_identifier) @callthis.field)
    property: (property_identifier) @callthis.callee))

; this.m()  -> direct method on the enclosing class
(call_expression
  function: (member_expression
    object: (this)
    property: (property_identifier) @callselfmethod.callee))

; Interfaces (types table). Captured separately from type aliases: only an
; interface can be implemented, and satisfaction is recorded per language from
; whichever construct that language uses.
(interface_declaration
  name: (type_identifier) @iface.name
  body: (interface_body) @iface.def)

(type_alias_declaration
  name: (type_identifier) @type.name
  value: (_) @type.def)

; Declared satisfaction: `class C implements I`. TypeScript states this outright,
; where Go leaves it structural — so satisfaction here is exact and carries no
; false positives, rather than being inferred from matching method names.
(class_declaration
  name: (type_identifier) @classbase.name
  (class_heritage
    (implements_clause (type_identifier) @classbase.base)))

; Inheritance: `class C extends B`. Same relation; a subclass satisfies whatever
; its base does.
(class_declaration
  name: (type_identifier) @classbase.name
  (class_heritage
    (extends_clause value: (identifier) @classbase.base)))

; Interface inheritance: `interface I extends J`.
(interface_declaration
  name: (type_identifier) @classbase.name
  (extends_type_clause (type_identifier) @classbase.base))

; Imports: namespace alias + module path.
(import_statement
  (import_clause
    (namespace_import (identifier) @import.alias))
  source: (string) @import.path)

(import_statement
  source: (string) @import.path)

; Named imports, for cross-file call resolution:
;   import { foo } from "./m"          -> import.symbol=foo (local == origin)
;   import { foo as bar } from "./m"   -> import.symbol=foo, import.alias.local=bar
; @import.symbol.src carries the module specifier so the parser can gate on it:
; only RELATIVE/in-repo specifiers seed a cross-file edge (a name imported from
; node:fs or a 3rd-party pkg must NOT link to a same-named local function).
(import_statement
  (import_clause
    (named_imports
      (import_specifier
        name: (identifier) @import.symbol)))
  source: (string) @import.symbol.src)
(import_statement
  (import_clause
    (named_imports
      (import_specifier
        name: (identifier) @import.symbol
        alias: (identifier) @import.alias.local)))
  source: (string) @import.symbol.src)
