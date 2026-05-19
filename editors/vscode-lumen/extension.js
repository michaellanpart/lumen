'use strict';

/**
 * Lumen VS Code extension.
 *
 * Activates a language client that connects to the `lumen-lsp` binary
 * (the Lumen Language Server) over stdio.  The binary must be on PATH or
 * located at the path specified by the `lumen.lspBinaryPath` setting.
 *
 * Features provided by the language server:
 *   - Diagnostics: parse errors + type-check errors shown as red squiggles.
 *   - Hover:       keyword / built-in / type documentation on mouse-over.
 *   - Completions: keyword, built-in function, and primitive-type completions.
 */

const vscode = require('vscode');
const { LanguageClient, TransportKind } = require('vscode-languageclient/node');

/** @type {LanguageClient | undefined} */
let client;

/**
 * Resolve the path to the lumen-lsp binary.
 * Preference order:
 *   1. `lumen.lspBinaryPath` workspace/user setting.
 *   2. `lumen-lsp` on PATH (the server will be found automatically when the
 *      working directory is the Lumen source tree after running `make`).
 */
function resolveBinaryPath() {
  const cfg = vscode.workspace.getConfiguration('lumen');
  const explicit = cfg.get('lspBinaryPath');
  return (explicit && explicit.trim()) ? explicit.trim() : 'lumen-lsp';
}

/**
 * @param {vscode.ExtensionContext} context
 */
function activate(context) {
  const serverExe = resolveBinaryPath();

  /** @type {import('vscode-languageclient/node').ServerOptions} */
  const serverOptions = {
    command: serverExe,
    args: [],
    transport: TransportKind.stdio,
  };

  /** @type {import('vscode-languageclient/node').LanguageClientOptions} */
  const clientOptions = {
    documentSelector: [{ scheme: 'file', language: 'lumen' }],
    synchronize: {
      fileEvents: vscode.workspace.createFileSystemWatcher('**/*.lm'),
    },
  };

  client = new LanguageClient(
    'lumen-lsp',
    'Lumen Language Server',
    serverOptions,
    clientOptions,
  );

  client.start();
  context.subscriptions.push(client);
}

function deactivate() {
  if (client) {
    return client.stop();
  }
  return undefined;
}

module.exports = { activate, deactivate };
