// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// U10: the synced keychain (task M0; Check 733).
//
// **What the screen does.** Writes one item through `expo-secure-store` -- the
// option the product app uses -- with the options that come closest to an
// iCloud-synced item on iOS, reads it back, and gives the owner the steps that
// settle the question on a second phone. The observation is typed into the
// field here and travels in the result block, so what lands in the draft is the
// owner's reading and not this screen's assumption.
//
// **Why the answer matters, on the screen.** Restore-from-the-synced-list (M3,
// K3) is the property that depends on it. If a synced keychain cannot hold the
// item, a second phone cannot restore the paired list from it, and the restore
// path needs another carrier -- which is the finding the plan has to have
// before M3 is written.

import { useState } from 'react';
import { Platform } from 'react-native';

import { resultBlock } from '../format.js';
import {
  ITEM_NAMES,
  KEYCHAIN_OPTIONS,
  RESTORE_MEANING,
  itemKey,
  readBack,
  writeOptions,
} from '../secure/store.js';
import { Button, CopyResult, Field, Head, Mono, Note, Page, Row } from '../ui/kit.js';

/** The value the probe writes: a length and a mark, never anything secret. */
function probeValue() {
  return `spike-733-${Date.now()}`;
}

export function U10Screen({ settings, secure }) {
  const [state, setState] = useState({ wrote: null, read: null, error: null, removed: false });
  const [observation, setObservation] = useState('');

  const key = itemKey(ITEM_NAMES.sync);

  /** Write the item with the sync-friendly options, then read it back. */
  async function writeAndRead() {
    try {
      const value = probeValue();
      await secure.put(key, value);
      const back = await secure.read(key);
      setState({ wrote: value, read: readBack({ value: back, expected: value }), error: null, removed: false });
    } catch (error) {
      setState({ wrote: null, read: null, error: error.message, removed: false });
    }
  }

  /** Read without writing, for the second-phone half of the probe. */
  async function readOnly() {
    try {
      const back = await secure.read(key);
      setState({ wrote: null, read: readBack({ value: back, expected: null }), error: null, removed: false });
    } catch (error) {
      setState({ wrote: null, read: null, error: error.message, removed: false });
    }
  }

  async function remove() {
    try {
      await secure.remove(key);
      setState({ wrote: null, read: null, error: null, removed: true });
    } catch (error) {
      setState({ wrote: null, read: null, error: error.message, removed: false });
    }
  }

  const options = writeOptions(Platform.OS);
  const block = resultBlock({
    question: 'U10',
    phone: settings.phone,
    fields: [
      ['Item name', key],
      ['Options asked for', Object.keys(options).length === 0 ? 'none (Android takes no accessibility level)' : JSON.stringify(options)],
      ['Accessibility level the SDK resolved', secure.accessible.name === null ? 'none on this platform' : `${secure.accessible.name} = ${String(secure.accessible.value)}`],
      ['Exported options that could sync it', `none — the SDK exposes ${KEYCHAIN_OPTIONS.levels.join(', ')} and no synchronizable attribute`],
      ['Write and read-back', state.error !== null ? `FAILED: ${state.error}` : state.read === null ? 'not run yet' : `${state.read.present ? `present, ${state.read.length} characters, matches ${state.read.matches ? 'yes' : 'no'}` : 'nothing read back'}`],
      ['Removed', state.removed ? 'yes' : 'no'],
      ['Owner observation (second phone, same Apple ID)', observation === '' ? 'UNVERIFIED — fill this in after the read on the second phone' : observation],
      ['What it means for restore (M3, K3)', RESTORE_MEANING],
    ],
  });

  return (
    <Page>
      <Head
        title="U10 — the synced keychain"
        question="Does the platform's synced keychain work with the secure-store option the app uses?"
      />
      <Note tone="warn">
        The finding, said before the probe: {KEYCHAIN_OPTIONS.why}. So this screen writes the closest item the SDK can
        write and hands you the read on the second phone, which is what settles it.
      </Note>

      <Button title="Write the probe and read it back" onPress={writeAndRead} />
      <Button title="Read it back without writing" onPress={readOnly} tone="quiet" />
      <Button title="Remove the probe" onPress={remove} tone="quiet" />

      {state.error === null ? null : <Note tone="warn">The store answered with: {state.error}</Note>}

      <Row label="Item" value={key} />
      <Row label="Options asked for" value={Object.keys(options).length === 0 ? 'none (Android)' : JSON.stringify(options)} />
      <Row label="Level the SDK resolved" value={secure.accessible.name === null ? 'none on this platform' : `${secure.accessible.name} = ${String(secure.accessible.value)}`} />
      <Row label="Read-back" value={state.read === null ? 'not run yet' : state.read.present ? `${state.read.length} characters, matches ${state.read.matches ? 'yes' : 'no'}` : 'nothing read back'} />

      <Note>Steps, on iOS:</Note>
      <Mono>1. Write the probe here. 2. Install this build on a second iPhone signed into the same Apple ID.</Mono>
      <Mono>3. Press &quot;Read it back without writing&quot; there. 4. Record what you saw in the field below.</Mono>
      <Note>
        The app&apos;s own items are written with WHEN_UNLOCKED_THIS_DEVICE_ONLY, which deliberately keeps them out of
        an iCloud backup; this probe writes WHEN_UNLOCKED instead, which is the closest the SDK gets to an item that
        can leave the phone. If the second phone reads nothing, that is the finding: iCloud Keychain synchronisation
        needs kSecAttrSynchronizable, which expo-secure-store does not expose.
      </Note>
      <Note>Steps, on Android:</Note>
      <Mono>Android has no synced keychain: Google&apos;s Auto Backup may restore the app&apos;s encrypted file to a reinstall of the same app on the same account, and it never restores to a second phone as a sync.</Mono>
      <Note>What the answer means: {RESTORE_MEANING}</Note>

      <Field
        label="Observation from the second phone"
        value={observation}
        onChangeText={setObservation}
        placeholder="what the second device read"
        hint="Typed here and carried in the block below; it is the reading the draft records."
      />

      <CopyResult block={block} what="U10" />
    </Page>
  );
}
