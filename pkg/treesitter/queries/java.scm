(method_declaration
  name: (identifier) @name
  body: (block) @body)

(class_declaration
  name: (identifier) @name
  body: (class_body) @body)

(method_invocation
  name: (identifier) @callee)

(import_declaration
  (scoped_identifier) @path)
