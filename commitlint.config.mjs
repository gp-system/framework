export default {
  extends: ['@commitlint/config-conventional'],
  ignores: [(message) => /Signed-off-by: dependabot\[bot\]/.test(message)],
}
