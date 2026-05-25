(function_definition
  declarator: (function_declarator
    declarator: (identifier) @name)
  body: (compound_statement) @body)

(call_expression
  function: (identifier) @callee)

(call_expression
  function: (field_expression
    argument: (identifier) @callee))

(class_specifier
  name: (type_identifier) @name
  body: (field_declaration_list) @definition)

(preproc_include
  path: (string_literal) @path)
