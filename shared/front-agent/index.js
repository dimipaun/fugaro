// The spec-shaping turn and the go (mobile plan, Task P5.4.4).
//
// This file is the module's only entry point. `./turn.js` is its whole body --
// the header there says what the module decides and why -- and a consumer
// imports from here, never around it, so the surface below is the module.

export {
  CONFIRM_CUE,
  DEFAULT_BIOMETRIC_ON_COSTLY,
  DEFAULT_COSTLY_MODES,
  DEFAULT_MAX_BRIEF,
  DEFAULT_MAX_TURNS,
  DEFAULT_TTL_MS,
  EVENT,
  ID_MAX,
  INPUT_REFUSALS,
  MAX_MAX_BRIEF,
  MAX_MAX_TURNS,
  MAX_TTL_MS,
  MIN_MAX_BRIEF,
  MIN_MAX_TURNS,
  MIN_TTL_MS,
  MODE_MAX,
  OUTCOME,
  PHASE,
  REFUSAL,
  REPO_MAX,
  ROLE,
  TEXT_MAX,
  TIME_MAX,
  TRANSITIONS,
  VERB,
  conversation,
  costlyStart,
  readBack,
  turn,
} from './turn.js';
