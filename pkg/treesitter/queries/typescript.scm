; Functions
(function_declaration
  name: (identifier) @name
  body: (statement_block) @body)

; Arrow functions assigned to variables: const foo = (...) => { ... }
(lexical_declaration
  (variable_declarator
    name: (identifier) @name
    value: (arrow_function
      body: (_) @body)))

; Methods
(method_definition
  name: (property_identifier) @name
  body: (statement_block) @body)

; Calls
(call_expression
  function: (identifier) @callee)

(call_expression
  function: (member_expression
    property: (property_identifier) @callee))

; Interfaces
(interface_declaration
  name: (type_identifier) @name
  body: (interface_body) @definition)

; Type aliases
(type_alias_declaration
  name: (type_identifier) @name
  value: (_) @definition)

; Imports
(import_statement
  source: (string) @path)

(import_clause
  (named_imports
    (import_specifier
      name: (identifier) @symbol)))
