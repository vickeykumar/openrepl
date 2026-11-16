// What the terminal's own error says is missing, for the App Store's hint (33-app-store.js). Only the last lines are read, and only
// the names a conda-forge package could have.

// the name of a module is not always the name of its package
export const ALIASES = { PIL: "pillow", yaml: "pyyaml", bs4: "beautifulsoup4", cv2: "opencv", sklearn: "scikit-learn", skimage: "scikit-image", dateutil: "python-dateutil", jwt: "pyjwt", OpenSSL: "pyopenssl", Crypto: "pycryptodome", serial: "pyserial" };

const NAME = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/;

// -> a package name, or "" when the text says nothing is missing. The newest error wins.
export function missingFrom(text) {
  const lines = String(text || "").split("\n").slice(-25);
  for (let i = lines.length - 1; i >= 0; i--) {
    const line = lines[i];
    let m = /No module named ['"]([^'"]+)['"]/.exec(line);
    if (m) {
      const top = m[1].split(".")[0];
      if (!NAME.test(top)) return "";
      return (ALIASES[top] || top).toLowerCase();
    }
    m = /(?:^|[\s:])([A-Za-z0-9][A-Za-z0-9._+-]{0,63}): (?:command )?not found\s*$/.exec(line);
    if (m && NAME.test(m[1])) return m[1].toLowerCase();
  }
  return "";
}
