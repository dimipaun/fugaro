// The database, behind a port (mobile plan, Task P3.6; Global Constraint 10,
// Voice D52's "fakes from task one").
//
// The app's mailbox client is the only place in the app that writes to the
// database, and its rules are the ones this task is reviewed for. None of them
// can be checked against a real database in CI -- there is no Firebase project
// in the runner, and Global Constraint 10 forbids a test that needs one -- so
// the database sits behind a two-method port:
//
//     write(path, value) -> { ok: true } | a refusal
//     read(path)         -> { ok: true, value } | a refusal
//
// and the real adapter (the Realtime Database SDK, with the account's token)
// is the app's own wiring, in `index.js`, where there is no decision to test.
//
// **Paths are the client's, and that is the point.** The port takes a path
// rather than a "write my inbox" verb, because "which paths does this client
// write" is the question P3.6 exists to answer and D113 answers with two. A
// port with one method per allowed node would answer it by construction and
// there would be nothing left to test; with paths, the fake records what it
// was asked to write and the test asserts the set is exactly the two the plan
// names.
//
// **`read` reads a subtree, the way the database does.** `read(outbox)` is one
// read of `mailbox/<uid>/outbox` and answers a map of message id to message,
// or `null` when the node is empty -- the shape a Realtime Database listener
// hands back. A message node handed over as itself (the heartbeat) comes back
// as itself. Every child is an OWN key of that map, whatever it is named: a
// listener hands back what `JSON.parse` builds, and `__proto__` is one of the
// keys P2.1's rules accept (see `own` below for why that is not a detail the
// accumulator can leave to plain assignment).
//
// **The fake models the rules' two refusals, because a re-send meets them.**
// `internal/budget/rules/rules.json.tmpl` writes the inbox node with
// `!data.exists()`, so a second write of a message that IS there is DENIED --
// and the phone cannot tell that from "you are not allowed", which is why the
// client treats it as landed rather than as a failure (`client.js`). It writes
// the ack with `data.parent().exists()`, so an ack for a message that is not
// there is denied too. A fake that accepted both would let a client that
// overwrote a message, or acked a message it never read, pass every test here.

/** What a failed call answers, and the two the rules themselves make. */
export const STORE_CODES = Object.freeze({
  /** The write did not land. A real adapter reports its own code here. */
  WRITE_FAILED: 'write-failed',
  /** The read did not land. */
  READ_FAILED: 'read-failed',
  /** The node already exists: the template's `!data.exists()`. */
  EXISTS: 'exists',
  /** The thing being written has no parent: the template's `data.parent().exists()`. */
  NO_PARENT: 'no-parent',
  /** The value the node's own rule refuses (an ack that is not `true`). */
  BAD_VALUE: 'bad-value',
});

// copy is a database's own serialisation, in miniature: what is written is
// stored as a JSON value and not as the object the caller kept, so a caller
// that mutates the object it wrote -- or the one it read -- cannot change what
// is stored. `undefined` is stored as itself (a node with no value), which is
// the one value JSON does not have.
function copy(value) {
  return value === undefined ? undefined : JSON.parse(JSON.stringify(value));
}

function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

// isAck says whether a path is the ack node the template's rule is about. The
// rule sits at `mailbox/$mb/outbox/$msg/acked` and NOWHERE ELSE, and `$msg` is
// any database key -- `acked` included. So the name alone is not the question:
// `inbox/acked` and `outbox/acked` are legal MESSAGE nodes, and a double that
// classified by suffix would refuse a legal message write (`bad-value`) and
// refuse to plant a legal outbox message (`no-parent`), answering about a
// database the rules do not describe. The position is checked with the name.
function isAck(path) {
  const parts = path.split('/');
  const last = parts.length - 1;
  return parts.length >= 3 && parts[last] === 'acked' && parts[last - 2] === 'outbox';
}

// own defines a child on a container as an OWN data property, which plain
// assignment cannot do for every legal key. `__proto__` is a legal Realtime
// Database key -- the schema's `keyField` and this port's `isDatabaseKey` both
// accept it, and `paths.test.js` holds the two rules to each other -- and the
// database delivers it as an own key, because a listener hands back what
// `JSON.parse` builds (`copy` below has the same semantics for the same
// reason). Assigning it would write THROUGH the key instead of adding a node:
// as a leaf it sets the result's prototype, so `Object.entries` sees no message
// and the client would never speak one the database had returned; one level up
// it lands on `Object.prototype` and pollutes the whole test process.
function own(container, key, value) {
  Object.defineProperty(container, key, {
    value,
    writable: true,
    enumerable: true,
    configurable: true,
  });
}

/**
 * The port's in-memory double: a map of path to value, the two refusals
 * `rules.json.tmpl` makes for the nodes the phone writes, and a log of every
 * call.
 *
 * It is not a database: nothing is durable, nothing is authenticated and no
 * rule other than the two named above is modelled. What it is for is asserting
 * the client's own decisions -- which paths it writes, what it writes to them,
 * what it does with a refusal -- with no network and no project.
 *
 * **Test-only accessors.** `writes()`, `reads()`, `valueAt()` and
 * `setFailures()` exist for the tests and for no other caller: the real
 * database cannot be enumerated and cannot be told to fail on demand. They are
 * here rather than in the test file because a fake that lives beside the port
 * is a fake that is used, and a fake written twice is two models of the
 * database.
 */
export class MemoryMailboxStore {
  #nodes = new Map();
  #writes = [];
  #reads = [];
  #failWrites;
  #failReads;
  #existsRefuses;

  /**
   * @param {{ failWrites?: boolean, failReads?: boolean, existsRefuses?: boolean }} [options]
   *   `failWrites`/`failReads` simulate an offline phone (the store is
   *   unreachable); `existsRefuses` models the template's `!data.exists()`
   *   and can be turned off to show what the client does when a re-write is
   *   quietly accepted instead.
   */
  constructor(options = {}) {
    this.#failWrites = options.failWrites === true;
    this.#failReads = options.failReads === true;
    this.#existsRefuses = options.existsRefuses !== false;
  }

  /**
   * Write one node.
   *
   * A write to the ack node -- `.../outbox/<msg>/acked`, the one position the
   * template has a rule for (`isAck` above) -- must be exactly `true` (the
   * template validates `newData.isBoolean() && newData.val() == true`) and
   * must have a parent. Every other path is written as given, including a
   * message whose id happens to be `acked`.
   */
  async write(path, value) {
    if (typeof path !== 'string' || path === '') return refuse(STORE_CODES.WRITE_FAILED, 'path');
    this.#writes.push(Object.freeze({ path, value }));
    if (this.#failWrites) return refuse(STORE_CODES.WRITE_FAILED, 'path');
    if (isAck(path)) {
      const parent = path.slice(0, -'/acked'.length);
      if (value !== true) return refuse(STORE_CODES.BAD_VALUE, 'value');
      if (!this.#nodes.has(parent)) return refuse(STORE_CODES.NO_PARENT, 'path');
    }
    if (this.#existsRefuses && this.#nodes.has(path)) return refuse(STORE_CODES.EXISTS, 'path');
    this.#nodes.set(path, copy(value));
    return Object.freeze({ ok: true });
  }

  /**
   * Read one node or subtree: the value at the path, a map of children when it
   * has any, or `null` when there is nothing there.
   */
  async read(path) {
    if (typeof path !== 'string' || path === '') return refuse(STORE_CODES.READ_FAILED, 'path');
    this.#reads.push(Object.freeze({ path }));
    if (this.#failReads) return refuse(STORE_CODES.READ_FAILED, 'path');
    return Object.freeze({ ok: true, value: this.#subtree(path) });
  }

  /**
   * Deliver a node the OTHER end wrote: the consumer's reply, the
   * orchestrator's heartbeat, the question a consumer is holding.
   *
   * **Why this is not `write`.** `write` is the PHONE's door and the one the
   * one-write-path scan (`write-path.test.js`) fences to `client.js`: it
   * models the two rules a phone write meets and records the attempt in
   * `writes()`. A harness that wants the tree to already hold the other end's
   * side is not making a phone write and must not appear to be one -- and the
   * real adapter has no such method, so nothing that reaches a database can be
   * built on this. What a caller gets for using it is the double's own read
   * semantics (subtrees, own `__proto__` keys, the ack's parent rule), which is
   * the whole reason to plant a delivery in the double rather than to keep a
   * simulant's map beside it.
   *
   * The delivery is NOT recorded in `writes()`: that log is "what the client
   * asked for", and a delivery the client never asked for would make the
   * one-write-path assertion in `client.test.js` a statement about the harness.
   */
  deliver(path, value) {
    if (typeof path !== 'string' || path === '') return refuse(STORE_CODES.WRITE_FAILED, 'path');
    this.#nodes.set(path, copy(value));
    return Object.freeze({ ok: true });
  }

  /** Every write the client asked for, in order, as `{ path, value }`. */
  writes() {
    return Object.freeze([...this.#writes]);
  }

  /** Every read the client asked for, in order, as `{ path }`. */
  reads() {
    return Object.freeze([...this.#reads]);
  }

  /** What is stored at a path, for a test's setup and its expectations. */
  valueAt(path) {
    return this.#nodes.has(path) ? this.#nodes.get(path) : null;
  }

  /** Turn the two failure modes on or off, for the offline and reconnect cases. */
  setFailures({ failWrites = this.#failWrites, failReads = this.#failReads } = {}) {
    this.#failWrites = failWrites;
    this.#failReads = failReads;
    return this;
  }

  // subtree is one database read: the exact node if it holds a value, the
  // children under it if it has any, and `null` if it has neither. The children
  // are built into a fresh object at every level -- a Realtime Database read
  // hands back a value and not a reference to the store.
  #subtree(path) {
    if (this.#nodes.has(path)) return copy(this.#nodes.get(path));
    const found = {};
    let any = false;
    for (const [node, value] of this.#nodes) {
      if (!node.startsWith(`${path}/`)) continue;
      const rest = node.slice(path.length + 1).split('/');
      let at = found;
      for (let i = 0; i < rest.length - 1; i++) {
        // A slot that does not hold a container becomes one. That is what the
        // database does when a write puts a child under a node that held a
        // scalar -- and it is also what keeps `own` from being asked to define
        // a property on a number.
        const child = Object.hasOwn(at, rest[i]) ? at[rest[i]] : undefined;
        if (child === null || typeof child !== 'object') own(at, rest[i], {});
        at = at[rest[i]];
      }
      own(at, rest[rest.length - 1], copy(value));
      any = true;
    }
    return any ? found : null;
  }
}
