(method
  name: (_) @name
  body: (_) @body)

(singleton_method
  name: (_) @name
  body: (_) @body)

(call
  method: (identifier) @callee)

(call
  method: (constant) @callee)

(class
  name: (constant) @name
  body: (body_statement) @body)
