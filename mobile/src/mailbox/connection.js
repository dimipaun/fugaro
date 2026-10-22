// The connection, behind a port (mobile plan, Task P6.10; D167, G21).
//
// **Why this is a port and not a read of the SDK.** D167's ruling is that the
// app **never relies on the SDK's offline write queue**: the Realtime Database
// client holds a write made while the device is offline and sends it on
// reconnect, so a command spoken in a tunnel runs an hour later -- or is refused
// at the other end as a replay, silently. The gate is what stops that, and the
// gate's first input is "is there a connection". That question is asked of the
// SDK (its `.info/connected` flag, or the platform's own network state) and the
// answer is handed to this module as a boolean, so that everything downstream of
// it -- the gate, the client -- is a pure decision a test can drive.
//
// **What the real implementation owes this port.** `isOnline()` answers whether
// the SDK would **send** a write right now. It must be false when the SDK would
// instead hold the write locally, and it must be the SDK's own notion rather
// than a second opinion: a client that asks the network stack while the SDK
// queues anyway has the same hole. The adapter is built in `mobile/index.js`,
// the one file that knows what a device is, and is passed to
// `createMailboxClient` as the `connection` option -- exactly as the database
// port is (`store.js`), and for the same reason: a module that reached for a
// global would be a module no test could drive.
//
// **Any answer that is not `true` is offline.** `evaluateGate` (see `gate.js`)
// treats a missing, `undefined` or `null` answer as "no connection", so a port
// that is wired wrong, or that throws its state away, refuses to send rather
// than sending into the void. Nothing here reads a network, a clock or a global.

/**
 * The port's in-memory double, in the shape `MemoryMailboxStore` and
 * `MemorySpeaker` have: a value a test sets, and an accessor for what the
 * module asked.
 *
 * It records every question, which is what makes "the gate was asked BEFORE
 * anything was written" a fact a test can assert rather than an ordering a
 * reader has to take on trust.
 */
export class MemoryConnection {
  #online;
  #asked = [];

  /**
   * @param {{ online?: boolean }} [options] `online` defaults to true when it is
   *   not given -- and only then: a test that is about something else is not
   *   silently a test of the offline gate, and a test that wrote `{online: 0}`
   *   is not silently a test of an ONLINE phone. The constructor follows
   *   `setOnline`'s rule (only `true` is connected) rather than testing for
   *   `false`, so the double cannot be more generous than `evaluateGate`.
   */
  constructor(options = {}) {
    this.#online = options.online === undefined ? true : options.online === true;
  }

  /**
   * Would the SDK send a write right now?
   *
   * @returns {boolean}
   */
  isOnline() {
    this.#asked.push(this.#online);
    return this.#online;
  }

  /** Turn the connection on or off, for the tunnel and reconnect cases. */
  setOnline(online = true) {
    this.#online = online === true;
    return this;
  }

  /**
   * What this port answered, in order, once per question. Test-only: a real
   * connection has no memory, and the client keeps none either.
   */
  asked() {
    return Object.freeze([...this.#asked]);
  }
}
