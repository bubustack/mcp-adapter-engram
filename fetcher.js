#!/usr/bin/env node
/* eslint-disable no-console */

/**
 * ESM MCP catalog scraper for Docker Hub's MCP Explore.
 *
 * Usage:
 *   node scrape-mcp-esm.mjs [--from 1] [--to 9] [--out mcp_services.json] [--cookie 'OptanonConsent=...; dckr-auth=...']
 *
 * Requires Node.js >= 18 (global fetch).
 */

// ---------------- CLI ----------------
const args = process.argv.slice(2);
const get = (flag, def) => {
  const i = args.indexOf(flag);
  return i >= 0 && i < args.length - 1 ? args[i + 1] : def;
};
const FROM = parseInt(get('--from', '1'), 10);
const TO = parseInt(get('--to', '9'), 10);
const OUT = get('--out', 'mcp_services.json');
const COOKIE = get('--cookie', '');

// ---------------- Fetch helpers ----------------
const EXPLORE_BASE = 'https://hub.docker.com/mcp/explore.data';
const ROUTES = '_routes=routes%2Fmcp%2Croutes%2Fmcp.explore._index';

function headers(extra = {}) {
  return {
    'User-Agent': 'Mozilla/5.0 (compatible; mcp-esm-scraper/1.0)',
    Accept: '*/*',
    ...(COOKIE ? { Cookie: COOKIE } : {}),
    ...extra,
  };
}

async function fetchText(url) {
  const res = await fetch(url, { headers: headers() });
  if (!res.ok) throw new Error(`${res.status} ${res.statusText} for ${url}`);
  return res.text();
}

async function fetchJSON(url) {
  const res = await fetch(url, { headers: headers() });
  if (!res.ok) {
    const body = await res.text().catch(() => '');
    throw new Error(`${res.status} ${res.statusText} for ${url}\n${body.slice(0, 300)}`);
  }
  return res.json();
}

// ---------------- Parser (decodes the compact "index-map" payload) ----------------

/**
 * @typedef {Object} MCPServer
 * @property {string} title
 * @property {string|null} [description]
 * @property {string|null} [image]
 * @property {string|null} [icon]
 * @property {string|null} [dateAdded]
 * @property {string|null} [owner]
 * @property {string|null} [category]
 * @property {string[]} [tags]
 * @property {string|null} [license]
 * @property {string|null} [readme]
 * @property {string|null} [toolsUrl]
 * @property {string|null} [source]
 * @property {string|null} [upstream]
 * @property {string|null} [ref]
 * @property {string[]} links
 * @property {Record<string, unknown>|null} [metadata]
 * @property {Array<Record<string, unknown>>|null} [secrets]
 * @property {Record<string, unknown>|null} [resources]
 * @property {Record<string, unknown>|null} [oauth]
 * @property {Record<string, unknown>|null} [remote]
 * @property {Record<string, unknown>|null} [command]
 * @property {string[]|null} [volumes]
 */

/**
 * Decode a single Explore response (string or already-parsed array) into MCPServer[].
 * It also surfaces extras like `secrets`, `resources`, `oauth`, `volumes`, etc. if present.
 *
 * @param {string|unknown[]} input
 * @returns {MCPServer[]}
 */
function parseMcpCatalog(input) {
  const table =
    typeof input === 'string' ? JSON.parse(input) :
    Array.isArray(input) ? input :
    (() => { throw new Error('Expected a JSON string or an array.'); })();

  const cache = new Map();

  const isIndexMap = (v) =>
    v &&
    typeof v === 'object' &&
    !Array.isArray(v) &&
    Object.keys(v).length > 0 &&
    Object.keys(v).every((k) => /^_\d+$/.test(k) && typeof v[k] === 'number');

  function getFromTable(idx) {
    if (idx == null || idx < 0) return null;
    if (cache.has(idx)) return /** @type {any} */ (cache.get(idx));
    const raw = table[idx];
    cache.set(idx, undefined); // optimistic to break cycles
    const resolved = decode(raw);
    cache.set(idx, resolved);
    return resolved;
  }

  function decode(val) {
    if (val === null || typeof val !== 'object') return val;
    if (Array.isArray(val)) {
      return val.map((el) => (typeof el === 'number' ? getFromTable(el) : decode(el)));
    }
    if (isIndexMap(val)) {
      const out = {};
      for (const [k, v] of Object.entries(val)) {
        const key = getFromTable(parseInt(k.slice(1), 10));
        const value = getFromTable(/** @type {number} */ (v));
        if (typeof key === 'string') out[key] = value;
      }
      return out;
    }
    const out = {};
    for (const [k, v] of Object.entries(val)) out[k] = decode(v);
    return out;
  }

  const decoded = table.map((_, i) => getFromTable(i));

  const seen = new Set();
  /** @type {MCPServer[]} */
  const servers = [];

  for (const item of decoded) {
    if (!item || typeof item !== 'object' || Array.isArray(item)) continue;
    const o = /** @type {Record<string, any>} */ (item);

    const looksLikeServer =
      (o.type === 'server' || o.toolsUrl || o.readme || o.source || o.upstream) &&
      typeof o.title === 'string';

    if (!looksLikeServer) continue;

    const id = `${o.title}|${o.toolsUrl ?? ''}|${o.readme ?? ''}|${o.source ?? ''}|${o.upstream ?? ''}`;
    if (seen.has(id)) continue;
    seen.add(id);

    const tags = Array.isArray(o.tags) ? o.tags.filter((t) => typeof t === 'string') : [];

    const links = [o.readme, o.toolsUrl, o.source, o.upstream, o.icon, o.image]
      .filter((x) => typeof x === 'string' && x.length > 0);

    // Collect extras if the server exposes them
    const secrets = Array.isArray(o.secrets) ? o.secrets.filter(Boolean) : null;
    const resources = o.resources && typeof o.resources === 'object' ? o.resources : null;
    const oauth = o.oauth && typeof o.oauth === 'object' ? o.oauth : null;
    const remote = o.remote && typeof o.remote === 'object' ? o.remote : null;
    const command = o.command && typeof o.command === 'object' ? o.command : null;
    const volumes = Array.isArray(o.volumes) ? o.volumes.filter((x) => typeof x === 'string') : null;

    servers.push({
      title: o.title,
      description: typeof o.description === 'string' ? o.description : null,
      image: typeof o.image === 'string' ? o.image : null,
      icon: typeof o.icon === 'string' ? o.icon : null,
      dateAdded: typeof o.dateAdded === 'string' ? o.dateAdded : null,
      owner: typeof o.owner === 'string' ? o.owner : null,
      category: typeof o.category === 'string' ? o.category : null,
      tags,
      license: typeof o.license === 'string' ? o.license : null,
      readme: typeof o.readme === 'string' ? o.readme : null,
      toolsUrl: typeof o.toolsUrl === 'string' ? o.toolsUrl : null,
      source: typeof o.source === 'string' ? o.source : null,
      upstream: typeof o.upstream === 'string' ? o.upstream : null,
      ref: typeof o.ref === 'string' ? o.ref : null,
      links,
      metadata: o.metadata && typeof o.metadata === 'object' ? o.metadata : null,
      secrets,
      resources,
      oauth,
      remote,
      command,
      volumes,
    });
  }

  return servers;
}

// ---------------- Tools JSON normalization ----------------

/**
 * Normalize a tool record to a stable shape and extract parameter schema if present.
 * Supports different field names used across servers.
 */
function normalizeTool(tool) {
  const name = tool?.name ?? tool?.tool ?? tool?.id ?? null;
  const description = typeof tool?.description === 'string' ? tool.description : null;

  // try several common fields for input schema:
  const schema =
    tool?.parameters ??
    tool?.input_schema ??
    tool?.inputSchema ??
    tool?.inputJsonSchema ??
    tool?.schema ??
    null;

  let properties = {};
  let required = [];

  if (schema && typeof schema === 'object') {
    if (schema.properties || schema.required || schema.type === 'object') {
      properties = schema.properties || {};
      required = Array.isArray(schema.required) ? schema.required : [];
    }
  }

  return {
    name,
    description,
    parameters: {
      properties,
      required,
    },
    // keep raw schema in case callers want the full shape
    _rawSchema: schema ?? null,
  };
}

/**
 * Fetch tools JSON for a given toolsUrl and return normalized tools array.
 */
async function fetchTools(toolsUrl) {
  const data = await fetchJSON(toolsUrl);

  const title = typeof data.title === 'string' ? data.title : (typeof data.name === 'string' ? data.name : null);
  const description = typeof data.description === 'string' ? data.description : null;
  const tools = Array.isArray(data.tools) ? data.tools : [];

  return {
    title,
    description,
    tools: tools.map(normalizeTool),
    _raw: data,
  };
}

// ---------------- Main ----------------

function extractToolsUrlsFromBlob(text) {
  // Works even if the blob is not valid JSON (we separately parse when needed)
  const re = /https?:\/\/desktop\.docker\.com\/mcp\/catalog\/v2\/tools\/[a-z0-9@._/-]+\.json/gi;
  const set = new Set((text.match(re) || []));
  return [...set];
}

function maybeTotalPages(text) {
  const m = text.match(/"totalPages"\s*,\s*(\d+)/);
  return m ? parseInt(m[1], 10) : null;
}

const delay = (ms) => new Promise((r) => setTimeout(r, ms));

async function main() {
  console.log(`Scanning MCP Explore pages ${FROM}..${TO}`);
  const pageBlobs = [];

  for (let p = FROM; p <= TO; p++) {
    const url = `${EXPLORE_BASE}?page=${p}&${ROUTES}`;
    try {
      const blob = await fetchText(url);
      pageBlobs.push(blob);
      const urls = extractToolsUrlsFromBlob(blob);
      console.log(`  • page ${p}: found ${urls.length} toolsUrl(s)`);
      await delay(120);
    } catch (e) {
      console.warn(`  ! page ${p} failed: ${e.message}`);
    }
  }

  // decode each page so we can capture servers + extras (secrets/resources/etc.)
  /** @type {MCPServer[]} */
  let servers = [];
  for (const blob of pageBlobs) {
    try {
      // some pages *are* valid JSON arrays; others are safe to JSON.parse too
      // If parse fails, the catch will skip this part; we still have toolsUrls later.
      const arr = JSON.parse(blob);
      const pageServers = parseMcpCatalog(arr);
      servers.push(...pageServers);
    } catch {
      // ignore; we’ll still pick up tools via regex
    }
  }

  // Also discover toolsUrls via regex to catch anything missed
  const toolsUrls = new Set();
  for (const blob of pageBlobs) {
    for (const u of extractToolsUrlsFromBlob(blob)) toolsUrls.add(u);
  }

  // Ensure each server has toolsUrl when possible by matching
  const byToolsUrl = new Map(servers.filter(s => s.toolsUrl).map(s => [s.toolsUrl, s]));
  for (const u of toolsUrls) {
    if (!byToolsUrl.has(u)) {
      // create a minimal stub to attach tools to
      servers.push({
        title: u.split('/').pop().replace(/\.json$/i, ''),
        description: null,
        image: null,
        icon: null,
        dateAdded: null,
        owner: null,
        category: null,
        tags: [],
        license: null,
        readme: null,
        toolsUrl: u,
        source: null,
        upstream: null,
        ref: null,
        links: [u],
        metadata: null,
        secrets: null,
        resources: null,
        oauth: null,
        remote: null,
        command: null,
        volumes: null,
      });
    }
  }

  // De-duplicate servers by toolsUrl+title
  const uniq = new Map();
  for (const s of servers) {
    const key = `${s.toolsUrl ?? ''}|${s.title ?? ''}`;
    if (!uniq.has(key)) uniq.set(key, s);
  }
  servers = [...uniq.values()].sort((a, b) => (a.title || '').localeCompare(b.title || ''));

  console.log(`Fetching tools for ${servers.length} server(s)...`);

  // Fetch tools with light concurrency
  const results = [];
  const errors = [];
  const CONC = 8;
  let idx = 0;

  async function worker() {
    while (idx < servers.length) {
      const i = idx++;
      const srv = servers[i];
      try {
        if (srv.toolsUrl) {
          const t = await fetchTools(srv.toolsUrl);
          results.push({
            ...srv,
            // prefer title/description coming from tools JSON if present
            title: t.title || srv.title,
            description: t.description || srv.description,
            tools: t.tools,
          });
        } else {
          // no toolsUrl exposed; still return server shell
          results.push({
            ...srv,
            tools: [],
          });
        }
      } catch (e) {
        errors.push({ server: srv.title, toolsUrl: srv.toolsUrl, error: String(e.message || e) });
      }
      await delay(60);
    }
  }
  await Promise.all(Array.from({ length: CONC }, worker));

  // Write output
  const payload = results;
  const fs = await import('node:fs/promises');
  await fs.writeFile(OUT, JSON.stringify(payload, null, 2), 'utf8');

  console.log(`\nSaved ${payload.length} servers to ${OUT}${errors.length ? `, with ${errors.length} error(s)` : ''}.`);
  if (errors.length) {
    const errPath = OUT.replace(/\.json$/i, '.errors.json');
    await fs.writeFile(errPath, JSON.stringify(errors, null, 2), 'utf8');
    console.log(`Error details: ${errPath}`);
  }

  // Nicety: show discovered totalPages if present
  const tp = pageBlobs.map(maybeTotalPages).find(Boolean);
  if (tp) console.log(`(Detected totalPages=${tp}. Re-run with --to ${tp} for full coverage.)`);
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
