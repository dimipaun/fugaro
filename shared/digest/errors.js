// The refusals the digest makes (mobile plan, Task P1.3).
//
// The task's rule is *fail closed*: an unknown field, a wrong type, an oversize
// field and every ambiguity is a **refusal with a distinct error code**, never
// a coercion. This file is the register of those codes -- one per class of
// refusal -- so that a caller can act on the code rather than on prose, and so
// that a later module adding a refusal has one place to add it.
//
// A refusal never echoes the value it refuses. The documents the digest reads
// carry agent-written text and, at the screen's end, material a redactor has
// already been over; a message that quoted the offending value would be a
// second path for it to travel. The message names the path and nothing else,
// and `DigestError#path` carries it structurally.

/**
 * One code per class of refusal. A caller switches on these; a test asserts
 * the exact code, because "it threw" is not the property the plan asks for.
 */
export const ERROR = Object.freeze({
  /** The digest was handed something that is not a JSONDoc object at all. */
  NOT_A_DOCUMENT: 'E_NOT_A_DOCUMENT',
  /** A field the contract does not name is present. Fail closed, not ignore. */
  UNKNOWN_FIELD: 'E_UNKNOWN_FIELD',
  /** A field the contract names is present with the wrong JSON type. */
  TYPE: 'E_TYPE',
  /** A string field is longer than the digest will carry. Refused, not cut. */
  OVERSIZE: 'E_OVERSIZE',
  /** A field that is an enumeration carries a value the contract does not name. */
  ENUM_UNKNOWN: 'E_ENUM_UNKNOWN',
  /** A finished entry carries neither `failed` nor `status`: unclassifiable. */
  UNCLASSIFIED: 'E_UNCLASSIFIED',
  /** A finished entry carries both and they disagree: the document contradicts itself. */
  CONTRADICTION: 'E_CONTRADICTION',
  /** A severity outside the ladder was ranked or compared. */
  SEVERITY_UNKNOWN: 'E_SEVERITY_UNKNOWN',
  /** The redactor was handed a secret list that is not a list of strings. */
  SECRETS_NOT_A_LIST: 'E_SECRETS_NOT_A_LIST',
});

/**
 * A refusal. `code` is one of `ERROR`; `path` is the JSON path of the field
 * that was refused (`$.runs[2].spent_usd`), so a caller can point at a
 * document's own defect without the message carrying the document's text.
 */
export class DigestError extends Error {
  constructor(code, path = '$') {
    super(`${code} at ${path}`);
    this.name = 'DigestError';
    this.code = code;
    this.path = path;
  }
}
