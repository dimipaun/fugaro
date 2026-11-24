// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The few pieces every screen of the spike is drawn from, so that a screen is
// about its question and not about its styling (task M0; Check 733).
//
// **The shells, and why they are here.** The four screens share one shape: a
// statement of what the question is, whatever controls make the observation
// possible, the reading, and one "Copy result" button. A screen that rolled its
// own version of that would drift from the others, and the drift would land in
// the owner's reading of a phone screen.

import * as Clipboard from 'expo-clipboard';
import { Pressable, ScrollView, StyleSheet, Text, TextInput, View } from 'react-native';

/** The page: every screen is one scrolling column. */
export function Page({ children }) {
  return (
    <ScrollView style={styles.page} contentContainerStyle={styles.pageContent}>
      {children}
    </ScrollView>
  );
}

/** A screen's heading, and the question it answers in a sentence. */
export function Head({ title, question }) {
  return (
    <View>
      <Text style={styles.title}>{title}</Text>
      <Text style={styles.question}>{question}</Text>
    </View>
  );
}

/** A section of explanatory text. */
export function Note({ children, tone = 'plain' }) {
  return <Text style={tone === 'warn' ? styles.noteWarn : styles.note}>{children}</Text>;
}

/** One labelled value, as a screen shows a reading. */
export function Row({ label, value, tone = 'plain' }) {
  return (
    <View style={styles.row}>
      <Text style={styles.rowLabel}>{label}</Text>
      <Text style={tone === 'warn' ? styles.rowValueWarn : styles.rowValue}>{String(value)}</Text>
    </View>
  );
}

/** A labelled text input, for the fields the owner types into. */
export function Field({ label, value, onChangeText, placeholder, hint }) {
  return (
    <View style={styles.field}>
      <Text style={styles.fieldLabel}>{label}</Text>
      <TextInput
        style={styles.input}
        value={value}
        onChangeText={onChangeText}
        placeholder={placeholder}
        autoCapitalize="none"
        autoCorrect={false}
      />
      {hint === undefined ? null : <Text style={styles.hint}>{hint}</Text>}
    </View>
  );
}

/** A button. `tone: 'quiet'` is the second one on a screen, never the only one. */
export function Button({ title, onPress, disabled = false, tone = 'plain' }) {
  return (
    <Pressable
      onPress={disabled ? undefined : onPress}
      style={[styles.button, tone === 'quiet' ? styles.buttonQuiet : null, disabled ? styles.buttonOff : null]}
    >
      <Text style={styles.buttonText}>{title}</Text>
    </Pressable>
  );
}

/**
 * The "Copy result" button, and the block it copies. The text is shown beside
 * it, so what is on the clipboard is what the owner saw before pressing -- and
 * a block that reads wrong is corrected on the screen instead of in the draft.
 */
export function CopyResult({ block, what }) {
  return (
    <View style={styles.copy}>
      <Button title={`Copy result (${what})`} onPress={() => Clipboard.setStringAsync(block)} />
      <Text style={styles.block}>{block}</Text>
    </View>
  );
}

/** A monospaced line, for a signature, a key or a raw payload. */
export function Mono({ children }) {
  return <Text style={styles.mono}>{String(children)}</Text>;
}

const styles = StyleSheet.create({
  page: { flex: 1, backgroundColor: '#0A0E16' },
  pageContent: { padding: 16, paddingBottom: 64, gap: 12 },
  title: { color: '#E8ECF4', fontSize: 22, fontWeight: '600' },
  question: { color: '#9AA5B8', fontSize: 15, marginTop: 4 },
  note: { color: '#9AA5B8', fontSize: 14, lineHeight: 20 },
  noteWarn: { color: '#F0B37E', fontSize: 14, lineHeight: 20 },
  row: { borderTopColor: '#1C2432', borderTopWidth: 1, paddingVertical: 8 },
  rowLabel: { color: '#6E7A8F', fontSize: 12, textTransform: 'uppercase' },
  rowValue: { color: '#E8ECF4', fontSize: 15, marginTop: 2 },
  rowValueWarn: { color: '#F0B37E', fontSize: 15, marginTop: 2 },
  field: { gap: 4 },
  fieldLabel: { color: '#6E7A8F', fontSize: 12, textTransform: 'uppercase' },
  input: {
    backgroundColor: '#131A26',
    borderColor: '#26303F',
    borderRadius: 6,
    borderWidth: 1,
    color: '#E8ECF4',
    paddingHorizontal: 10,
    paddingVertical: 8,
  },
  hint: { color: '#6E7A8F', fontSize: 12 },
  button: {
    alignItems: 'center',
    backgroundColor: '#2B5FD9',
    borderRadius: 8,
    marginTop: 8,
    paddingHorizontal: 14,
    paddingVertical: 12,
  },
  buttonQuiet: { backgroundColor: '#1C2432' },
  buttonOff: { opacity: 0.4 },
  buttonText: { color: '#FFFFFF', fontSize: 15, fontWeight: '600' },
  copy: { borderTopColor: '#1C2432', borderTopWidth: 1, paddingTop: 8 },
  block: {
    backgroundColor: '#0F1520',
    borderColor: '#26303F',
    borderRadius: 6,
    borderWidth: 1,
    color: '#B9C4D4',
    fontFamily: 'monospace',
    fontSize: 12,
    marginTop: 8,
    padding: 8,
  },
  mono: { color: '#B9C4D4', fontFamily: 'monospace', fontSize: 12 },
});
