// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The one screen with fields: the identity project's Web API key, the project
// id, and the name of the phone in the owner's hand (task M0; Check 733).
//
// **Nothing typed here is written to disk, and nothing here is committed.**
// The key names a Firebase project and is not a credential in Firebase's model
// (the rules decide, not the key), but it is the owner's project all the same:
// it is held in this screen's state for as long as the app runs, and it is
// deliberately *not* put in the secure store or in AsyncStorage -- a spike that
// persisted a project's key on a handset would be a spike that left one behind.
// The provider client ids are the other way round: they are public by design,
// so they come from the app config at build time (`app.config.js` reads them
// from the environment, and `.env.example` names the variables).

import { Field, Head, Note, Page, Row } from '../ui/kit.js';

/** The screen: three fields, and the sentences that say what may be typed into them. */
export function SettingsScreen({ settings, onChange }) {
  const set = (key) => (value) => onChange({ ...settings, [key]: value });
  return (
    <Page>
      <Head
        title="Settings"
        question="The identity project the exchange is asked, and the phone these readings are from."
      />
      <Field
        label="Firebase Web API key"
        value={settings.apiKey}
        onChangeText={set('apiKey')}
        placeholder="AIza… (the identity project's Web API key)"
        hint="Held in memory for this run only. Never committed; not written to the keychain or to storage."
      />
      <Field
        label="Firebase project id"
        value={settings.projectId}
        onChangeText={set('projectId')}
        placeholder="the identity project's id"
        hint="Recorded in the result blocks so a reading says which project it was taken against."
      />
      <Field
        label="This phone, as the draft names it"
        value={settings.phone}
        onChangeText={set('phone')}
        placeholder="iPhone 15 / Pixel 9"
        hint="Every result block is labelled with it: the draft has a section per phone."
      />
      <Note>
        Check 733 wants the same four readings on an iPhone and on an Android phone. Run this build on both, type a
        name for the handset here, and paste each screen&apos;s block into that phone&apos;s table in{' '}
        app-spike-DRAFT.md.
      </Note>
      <Note tone="warn">
        What this spike never does: it does not call your Fugaro installation, credsvc or any pairing route, it stores
        no Fugaro credential, and it shows no provider token, no private key and no keychain value.
      </Note>
      <Row label="Platform" value={settings.platform} />
    </Page>
  );
}
