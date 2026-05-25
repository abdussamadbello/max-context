; Functions
(function_item
  name: (identifier) @name
  body: (block) @body)

; Calls
(call_expression
  function: (identifier) @callee)

(call_expression
  function: (field_expression
    field: (identifier) @callee))

; Types (struct, enum, trait)
(struct_item
  name: (type_identifier) @name
  body: (declaration_list) @definition)

(enum_item
  name: (type_identifier) @name
  body: (enum_variant_list) @definition)

(trait_item
  name: (type_identifier) @name
  body: (declaration_list) @definition)

; Imports
(use_declaration
  argument: (scoped_identifier) @path)

(use_declaration
  argument: (identifier) @symbol)
