// Realistic TypeScript fixture for resolver measurement.
// Exercises: field types, typed params, return types, this.field.m(),
// this.m(), new X() locals, return-typed locals.

export class Conn {
  query(sql: string): Row[] {
    return [];
  }
  close(): void {}
}

export class Row {
  value(): string {
    return "";
  }
}

export class Repository {
  private conn: Conn;

  constructor(conn: Conn) {
    this.conn = conn;
  }

  load(id: string): Row[] {
    const rows = this.conn.query("select"); // field receiver -> Conn.query
    this.cleanup();                          // this.method -> Repository.cleanup
    return rows;
  }

  cleanup(): void {
    this.conn.close(); // field receiver -> Conn.close
  }
}

function newConn(): Conn {
  return new Conn();
}

export class Service {
  private repo: Repository;

  run(id: string): void {
    const c = newConn(); // return-typed local -> Conn
    c.query("x");        // -> Conn.query
    const fresh = new Conn(); // new local -> Conn
    fresh.close();            // -> Conn.close
    this.repo.load(id);       // field receiver -> Repository.load
  }
}
