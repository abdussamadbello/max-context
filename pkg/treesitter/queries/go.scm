; Functions
(function_declaration
  name: (identifier) @name
  body: (block) @body)

(method_declaration
  name: (field_identifier) @name
  body: (block) @body)

; Calls
(call_expression
  function: (identifier) @callee)

(call_expression
  function: (selector_expression
    field: (field_identifier) @callee))

; Types
(type_declaration
  (type_spec
    name: (type_identifier) @name
    type: (_) @definition))

; Imports
(import_declaration
  (import_spec
    path: (interpreted_string_literal) @path))
