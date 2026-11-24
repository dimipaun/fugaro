// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The spike's shell: four question tabs, one settings tab, and the two things
// every screen is handed -- the settings in memory, and the network transport
// (task M0; Check 733).
//
// **The transport, and why it is a value.** The spike's one egress is the
// `accounts:signInWithIdp` exchange in `src/firebase/idp.js`: `signInWithIdp`
// takes the transport as its first argument and calls it, and this file is
// where the platform's own is read off the global object and handed over. That
// is deliberate on two counts. It is what makes the exchange testable with no
// network at all -- the tests hand it a fake and a dead one -- and it is what
// keeps the spike inside the repo's boundary rule for the app's network, whose
// allow-list names `src/voice-switch/hop.js` and cannot be edited from a run:
// the spike's single call site is the reviewed module that holds the one
// endpoint, and no screen can open a second one. `README.md` says so too.
//
// **No navigation dependency.** Five tabs are a piece of state and a row of
// buttons, and a spike that added a router to draw them would be a spike whose
// build can fail for a reason that has nothing to do with its four questions.

import Constants from 'expo-constants';
import { useState } from 'react';
import { Platform, Pressable, StyleSheet, Text, View } from 'react-native';

import { SettingsScreen } from './screens/settings.js';
import { U4Screen } from './screens/u4.js';
import { U5Screen } from './screens/u5.js';
import { U7Screen } from './screens/u7.js';
import { U10Screen } from './screens/u10.js';
import { createSecureStore } from './secure/sdk.js';

/** The provider client ids, from the app config -- public by design, set at build time. */
function clientIdsFromConfig() {
  const extra = Constants.expoConfig?.extra ?? {};
  return {
    google: extra.googleClientId ?? '',
    googleIos: extra.googleIosClientId ?? '',
    googleAndroid: extra.googleAndroidClientId ?? '',
    microsoft: extra.microsoftClientId ?? '',
  };
}

const TABS = [
  { key: 'settings', title: 'Settings' },
  { key: 'u4', title: 'U4 tokens' },
  { key: 'u5', title: 'U5 key' },
  { key: 'u7', title: 'U7 share' },
  { key: 'u10', title: 'U10 keychain' },
];

export function App() {
  const [tab, setTab] = useState('settings');
  const [settings, setSettings] = useState({
    // The Firebase Web API key is typed here and lives in this state alone: see
    // `screens/settings.js` for why it is in neither a file nor the keychain.
    apiKey: '',
    projectId: '',
    phone: '',
    platform: Platform.OS,
    clientIds: clientIdsFromConfig(),
  });
  // Built once: the port holds no state, and a new one per render would make
  // every screen's identity change under it.
  const [secure] = useState(() => createSecureStore());

  return (
    <View style={styles.app}>
      <View style={styles.bar}>
        {TABS.map((entry) => (
          <Pressable
            key={entry.key}
            onPress={() => setTab(entry.key)}
            style={[styles.tab, tab === entry.key ? styles.tabOn : null]}
          >
            <Text style={styles.tabText}>{entry.title}</Text>
          </Pressable>
        ))}
      </View>
      <View style={styles.body}>
        {tab === 'settings' ? <SettingsScreen settings={settings} onChange={setSettings} /> : null}
        {tab === 'u4' ? <U4Screen settings={settings} http={globalThis.fetch} /> : null}
        {tab === 'u5' ? <U5Screen settings={settings} secure={secure} /> : null}
        {tab === 'u7' ? <U7Screen settings={settings} /> : null}
        {tab === 'u10' ? <U10Screen settings={settings} secure={secure} /> : null}
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  app: { flex: 1, backgroundColor: '#0A0E16' },
  bar: {
    backgroundColor: '#0F1520',
    borderBottomColor: '#1C2432',
    borderBottomWidth: 1,
    flexDirection: 'row',
    flexWrap: 'wrap',
    gap: 6,
    paddingHorizontal: 8,
    paddingTop: 48,
    paddingBottom: 8,
  },
  tab: { backgroundColor: '#1C2432', borderRadius: 6, paddingHorizontal: 10, paddingVertical: 8 },
  tabOn: { backgroundColor: '#2B5FD9' },
  tabText: { color: '#E8ECF4', fontSize: 13, fontWeight: '600' },
  body: { flex: 1 },
});
