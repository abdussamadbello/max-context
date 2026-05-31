# Realistic Python fixture for resolver measurement.
# Exercises: annotated class fields, __init__ self-assignment, typed params,
# self.field.method(), self.method(), constructor-typed locals.


class Conn:
    def query(self, sql):
        return []

    def close(self):
        pass


class Row:
    def value(self):
        return ""


class Repository:
    conn: Conn

    def __init__(self):
        self.cache = Conn()

    def load(self, id):
        self.conn.query("select")   # annotated field -> Conn.query
        self.cache.query("select")  # __init__ assignment -> Conn.query
        self.cleanup()              # self.method -> Repository.cleanup

    def cleanup(self):
        self.conn.close()           # annotated field -> Conn.close


class Service:
    def run(self, c: Conn):
        c.query("x")     # typed param -> Conn.query
        c.close()        # typed param -> Conn.close
        local = Conn()
        local.query("y") # constructor-typed local -> Conn.query
