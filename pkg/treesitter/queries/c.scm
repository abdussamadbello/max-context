(function_definition
  declarator: (identifier) @name
  body: (compound_statement) @body)

(call_expression
  function: (identifier) @callee)

(call_expression
  function: (field_expression
    argument: (identifier) @callee))

(preproc_include
  path: (string_literal) @path)
