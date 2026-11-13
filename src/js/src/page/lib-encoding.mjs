// Text to base64 for saving a file: the text as UTF-8 bytes. btoa alone takes only Latin1 characters, and throws
// "InvalidCharacterError" for anything else (an ellipsis, a dash, an arrow, an emoji, a letter of another language); and for the
// Latin1 letters it makes one byte where UTF-8 has two, so a file with an "é" was saved in a form that does not read back as UTF-8.
// A load decodes the bytes as UTF-8 (TextDecoder in 07-file-browser.js and 24-large-file-viewer.js), so this is its other half.
// The bytes go to btoa in pieces, so a big file does not overflow the arguments of String.fromCharCode.
export function textToBase64(text) {
  const bytes = new TextEncoder().encode(String(text));
  let binary = "";
  for (let i = 0; i < bytes.length; i += 0x8000) {
    binary += String.fromCharCode.apply(null, bytes.subarray(i, i + 0x8000));
  }
  return btoa(binary);
}
