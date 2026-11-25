// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// U5: the device signing key (task M0; Check 733).
//
// **The finding this screen exists to produce.** No stock Expo package mints an
// asymmetric key in platform hardware, so the key this stack can hold is a
// software key -- and U5's own wording says that answer is valid: "otherwise
// the key is software and the plan says so". So the screen says so, in the
// four answers the draft asks for (algorithm, hardware-backed, exportable, and
// a signature over a fixed message with its verification), and it names what a
// hardware key would need.
//
// **What is stored where.** The software key's private bytes are written to the
// *app's* secure store -- the same keychain/Keystore the product app uses for
// its items, which protects it at rest -- and the read-back proves it is there.
// That is a materially weaker thing than a non-exportable key, and the screen
// says which is which rather than blurring them: hardware-protected *at rest*
// is not hardware-*generated*, and only the second one is non-exportable.

import { ed25519 } from '@noble/curves/ed25519.js';
import { useState } from 'react';

import { FIXED_MESSAGE, HARDWARE_PROBE, generate, hex, report, signAndVerify } from '../devicekey/software.js';
import { resultBlock } from '../format.js';
import { ITEM_NAMES, itemKey, readBack } from '../secure/store.js';
import { Button, CopyResult, Head, Mono, Note, Page, Row } from '../ui/kit.js';

export function U5Screen({ settings, secure }) {
  const [state, setState] = useState({ key: null, proof: null, stored: null, error: null });

  /** Mint a key, sign the fixed message, verify it, and put the private half in the app's store. */
  async function mint() {
    try {
      const key = generate(ed25519);
      const proof = signAndVerify(ed25519, key, FIXED_MESSAGE);
      const value = hex(key.privateKey);
      await secure.put(itemKey(ITEM_NAMES.deviceKey), value);
      const back = await secure.read(itemKey(ITEM_NAMES.deviceKey));
      setState({ key, proof, stored: readBack({ value: back, expected: value }), error: null });
    } catch (error) {
      setState({ key: null, proof: null, stored: null, error: error.message });
    }
  }

  const read = state.key === null ? null : report(state.key, state.proof);
  const block = resultBlock({
    question: 'U5',
    phone: settings.phone,
    fields: [
      ['Algorithm', read === null ? 'UNVERIFIED (no key minted yet)' : read.algorithm],
      [
        'Hardware-backed, non-exportable key in this stack',
        read === null
          ? 'UNVERIFIED'
          : `no — ${HARDWARE_PROBE.why}`,
      ],
      ['What was tried', HARDWARE_PROBE.tried],
      ['Fallback key', read === null ? 'UNVERIFIED' : `software Ed25519 from @noble/curves; hardware-backed ${read.hardwareBacked ? 'yes' : 'no'}, exportable ${read.exportable ? 'yes' : 'no'}`],
      ['Public key', read === null ? 'UNVERIFIED' : read.publicKey],
      ['Message', read === null ? 'UNVERIFIED' : read.message],
      ['Signature', read === null ? 'UNVERIFIED' : `${read.signature} (${read.signatureBytes} bytes)`],
      ['Signature verified by the public key', read === null ? 'UNVERIFIED' : read.verified ? 'yes' : 'NO — the signature did not verify'],
      ['Private key stored in the app secure store', state.stored === null ? 'UNVERIFIED' : state.stored.present ? `yes, read back ${state.stored.length} characters, matches ${state.stored.matches ? 'yes' : 'NO'}` : 'no — the write did not read back'],
      ['Private key bytes shown', 'never, on any screen and in any block'],
    ],
  });

  return (
    <Page>
      <Head
        title="U5 — the device key"
        question="Can this stack hold a hardware-backed, non-exportable signing key, or is the key software?"
      />
      <Note tone="warn">
        The finding, before you press anything: no stock Expo package generates an asymmetric key in platform
        hardware. {HARDWARE_PROBE.why}. So the key is software, and this screen says so — a hardware key would need a
        native module or a config plugin that brings one.
      </Note>

      <Button title="Mint a key and sign the fixed message" onPress={mint} />

      {state.error === null ? null : <Note tone="warn">The probe failed: {state.error}</Note>}

      <Row label="Algorithm" value={read === null ? 'not minted yet' : read.algorithm} />
      <Row label="Hardware-backed" value={read === null ? 'not minted yet' : String(read.hardwareBacked)} />
      <Row label="Exportable" value={read === null ? 'not minted yet' : String(read.exportable)} />
      <Row label="Public key" value={read === null ? 'not minted yet' : read.publicKey} />
      <Row
        label="Signature verified"
        value={read === null ? 'not minted yet' : read.verified ? 'yes' : 'NO'}
        tone={read !== null && !read.verified ? 'warn' : 'plain'}
      />
      <Row
        label="Private key in the secure store"
        value={state.stored === null ? 'not written yet' : state.stored.present ? `${state.stored.length} characters, matches ${state.stored.matches ? 'yes' : 'NO'}` : 'no'}
      />
      <Mono>{read === null ? 'no signature yet' : read.signature}</Mono>

      <Note>
        What M3 takes from this: the plan&apos;s device binding is a signature the phone can make, and this key can make
        one. It is software, so its bytes can be copied by anyone who can run code in this app — which is why the
        draft records it as software rather than as the hardware binding the spec assumed.
      </Note>

      <CopyResult block={block} what="U5" />
    </Page>
  );
}
