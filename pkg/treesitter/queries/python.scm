; Functions
(function_definition
  name: (identifier) @name
  body: (block) @body)

; Calls
(call
  function: (identifier) @callee)

(call
  function: (attribute
    attribute: (identifier) @callee))

; Classes (types)
(class_definition
  name: (identifier) @name
  body: (block) @body)

; Imports
(import_statement
  name: (dotted_name) @path)

(import_from_statement
  module_name: (dotted_name) @path
  name: (dotted_name) @symbol)
