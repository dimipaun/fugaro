// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// U7: the share route (task M0; Check 733).
//
// **What the screen is for.** To open the share sheet from another app with a
// text selection and see what arrives here: the text, its length, the package
// (Android) or bundle (iOS) if the platform exposes it, and whether the
// 20,000-character probe arrives in one piece. The probe is copied to the
// clipboard by the button below, so it can be pasted into any app that shares
// text and shared back.
//
// **The route, said plainly on the screen.** Expo's managed configuration
// cannot deliver a shared text: no Expo API reads Android's `Intent.EXTRA_TEXT`
// and nothing in Expo adds an iOS share extension. `expo-share-intent`'s config
// plugin does both at prebuild, which is why this project depends on it -- and
// if the plugin was not applied to the build in your hand, this screen shows
// the module's own error rather than an empty reading. The iOS notes are the
// owner's console work: an App Group, a bundle identifier for the extension,
// and a provisioning profile that carries them (README, "The share extension").

import { useShareIntent } from 'expo-share-intent';
import * as Clipboard from 'expo-clipboard';
import { useState } from 'react';
import { Platform } from 'react-native';

import { resultBlock } from '../format.js';
import { LONG_PASTE_LENGTH, describe, longPaste, parseShare, preview } from '../share/payload.js';
import { Button, CopyResult, Head, Mono, Note, Page, Row } from '../ui/kit.js';

/** The plugin's own answer, bounded: a shared page or a 20,000-character paste is not printed whole. */
function rawPreview(shareIntent) {
  try {
    const text = JSON.stringify(shareIntent ?? null);
    return text.length <= 500 ? text : `${text.slice(0, 500)}…`;
  } catch {
    return 'the payload could not be rendered';
  }
}

export function U7Screen({ settings }) {
  const { hasShareIntent, shareIntent, resetShareIntent, error } = useShareIntent({ resetOnBackground: false });
  const [copied, setCopied] = useState(false);
  const read = parseShare(hasShareIntent ? shareIntent : null, { platform: Platform.OS });
  const lines = describe(read, { platform: Platform.OS });

  const block = resultBlock({
    question: 'U7',
    phone: settings.phone,
    fields: [
      ['Route', 'expo-share-intent\'s config plugin: the iOS share extension and the Android ACTION_SEND text/plain intent filter, both added at prebuild'],
      ['Stock Expo alternative', 'none: no Expo API reads Intent.EXTRA_TEXT and nothing in Expo adds an iOS share extension, so the text cannot reach the app without a native module'],
      ...lines,
      ['Platform error', error ?? 'none'],
    ],
  });

  return (
    <Page>
      <Head
        title="U7 — the share route"
        question="Does a text selection reach the app from another app's share sheet, under which package or bundle, and whole?"
      />
      <Note>
        Copy the {LONG_PASTE_LENGTH}-character probe, paste it into any app that can share text (Notes, Messages, a
        browser), and share it back to this app. The probe carries a mark at each end, so a share that dropped its
        tail is visible and not only countable.
      </Note>

      <Button
        title={`Copy the ${LONG_PASTE_LENGTH}-character probe`}
        onPress={async () => {
          await Clipboard.setStringAsync(longPaste());
          setCopied(true);
        }}
      />
      <Row label="Probe copied" value={copied ? 'yes — paste it somewhere and share it back' : 'not yet'} />
      <Button title="Clear the shared content" onPress={() => resetShareIntent()} tone="quiet" />

      {lines.map(([label, value]) => (
        <Row key={label} label={label} value={value} />
      ))}

      <Row label="Arrived at all" value={hasShareIntent ? 'yes' : 'no — nothing has been shared into the app yet'} />
      <Row label="Text (first 160 characters)" value={read.present ? preview(read.text) : 'UNVERIFIED'} />
      <Note>Raw payload, as the plugin handed it over (bounded, and truncated if longer than 500 characters):</Note>
      <Mono>{rawPreview(hasShareIntent ? shareIntent : null)}</Mono>
      {error === undefined || error === null ? null : <Note tone="warn">The share module answered with: {error}</Note>}

      <Note tone="warn">
        iOS needs console work this build cannot do for you: an App Group shared by the app and the extension, a
        bundle identifier for the extension, and a provisioning profile carrying both. Without them the extension
        installs and never launches. Android needs nothing beyond the plugin&apos;s intent filter.
      </Note>

      <CopyResult block={block} what="U7" />
    </Page>
  );
}
