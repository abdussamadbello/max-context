; ---- Python ----
; Dotted captures feed the dedicated parsePython path.

; Classes
(class_definition
  name: (identifier) @class.name
  body: (block) @class.body)

; Class base classes (inheritance): class Client(BaseClient): ...
; Each base identifier in the superclasses list pairs with the class name.
(class_definition
  name: (identifier) @classbase.name
  superclasses: (argument_list (identifier) @classbase.base))

; Functions / methods (+ optional return-type hint). Method-ness is decided by
; enclosing-class line range in the parser.
(function_definition
  name: (identifier) @func.name
  return_type: (type (identifier) @func.result.type)?
  body: (block) @func.body)

; Typed parameters: def f(self, req: Request)
(typed_parameter
  (identifier) @param.name
  type: (type (identifier) @param.type))

; Element-typed collections: `ns: List[Notifier]`, `ns: Sequence[Notifier]`. The
; identifier's own type is the collection; a `for` binding over it needs the
; ELEMENT type. Without this a protocol held in a list is never reached — the
; same gap the Go and TypeScript fixtures each exposed in turn.
(typed_parameter
  (identifier) @elem.name
  type: (type (generic_type
    (identifier) @_container
    (type_parameter (type (identifier) @elem.type)))))

; for n in ns: — bind n to ns's element type.
(for_statement
  left: (identifier) @range.name
  right: (identifier) @range.src)

; Module-level constants/vars: NAME = value at file scope. Anchored at module
; scope (and module-level try/except/if/else guards — the common optional-import
; and version-gate fallback patterns) so config constants like DEFAULT_TIMEOUT_CONFIG
; are searchable, while assignments inside functions/classes (locals/attrs) are NOT
; captured. Each form lists the assignment as a DIRECT child of the named block, so
; nesting inside a def/class never matches.
(module
  (expression_statement
    (assignment
      left: (identifier) @const.name)))
; try: NAME = ... except: NAME = ...  (optional-dependency fallback)
(module
  (try_statement
    body: (block
      (expression_statement
        (assignment left: (identifier) @const.name)))))
(module
  (try_statement
    (except_clause
      (block
        (expression_statement
          (assignment left: (identifier) @const.name))))))
; if/else NAME = ...  (version / platform gate)
(module
  (if_statement
    consequence: (block
      (expression_statement
        (assignment left: (identifier) @const.name)))))
(module
  (if_statement
    alternative: (else_clause
      (block
        (expression_statement
          (assignment left: (identifier) @const.name))))))

; Class-level field annotation: x: Conn
(class_definition
  body: (block
    (expression_statement
      (assignment
        left: (identifier) @field.name
        type: (type (identifier) @field.type)))))

; self.field = Ctor()  -> field type = constructor name (best-effort)
(assignment
  left: (attribute
    object: (identifier) @selfassign.recv
    attribute: (identifier) @selfassign.field)
  right: (call function: (identifier) @selfassign.ctor))

; x = Ctor() / x = make()  -> return/constructor-typed local
(assignment
  left: (identifier) @localcall.name
  right: (call function: (identifier) @localcall.callee))

; Bare calls: foo()
(call
  function: (identifier) @call.callee)

; Attribute calls: x.m()  -> recv=x, callee=m
(call
  function: (attribute
    object: (identifier) @call.recv
    attribute: (identifier) @call.callee))

; self.field.method()  -> field receiver: base=self, field, callee
(call
  function: (attribute
    object: (attribute
      object: (identifier) @callself.recv
      attribute: (identifier) @callself.field)
    attribute: (identifier) @callself.callee))

; self.method()  -> direct method on the enclosing class
(call
  function: (attribute
    object: (identifier) @callselfmethod.recv
    attribute: (identifier) @callselfmethod.callee))

; super().method() -> the method the BASE class provides. Distinct from
; self.method(): the call site wrote super precisely to skip an override here.
(call
  function: (attribute
    object: (call function: (identifier) @callsuper.recv)
    attribute: (identifier) @callsuper.callee))

; Imports
(import_statement
  name: (dotted_name) @path)

(import_from_statement
  module_name: (dotted_name) @path)

; Imported-by-name symbols, for cross-file call resolution:
;   from m import f          -> import.symbol=f (local name == original)
;   from m import f as g     -> import.symbol=f, import.alias=g
; A bare call to the local name (f or g) then resolves to the original symbol f.
; @import.symbol.mod / .relmod carry the source module so the parser can gate:
; only RELATIVE (from .mod) or in-repo absolute modules seed a cross-file edge —
; a name from a stdlib/3rd-party module (urllib.parse) must NOT link locally.
(import_from_statement
  module_name: (dotted_name) @import.symbol.mod
  name: (dotted_name) @import.symbol)
(import_from_statement
  module_name: (dotted_name) @import.symbol.mod
  name: (aliased_import
    name: (dotted_name) @import.symbol
    alias: (identifier) @import.alias))
(import_from_statement
  module_name: (relative_import) @import.symbol.relmod
  name: (dotted_name) @import.symbol)
(import_from_statement
  module_name: (relative_import) @import.symbol.relmod
  name: (aliased_import
    name: (dotted_name) @import.symbol
    alias: (identifier) @import.alias))
