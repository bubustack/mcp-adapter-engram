#!/usr/bin/env node
/**
 * Generates Engram example manifests and documentation rows
 * from the curated GitHub MCP catalog proxy (`github-mcp-page`)
 * and the Docker MCP Explore scrape (`mcp_services.json`).
 *
 * Usage:
 *   node scripts/generate-catalog.js
 */

const fs = require('fs');
const path = require('path');

const ROOT = path.resolve(__dirname, '..');
const SERVERS_DIR = path.join(ROOT, 'examples', 'servers');
const DOC_PATH = path.join(ROOT, 'docs', 'server-catalog.md');
const GITHUB_PAGE_PATH = path.join(ROOT, 'github-mcp-page');
const MCP_SERVICES_PATH = path.join(ROOT, 'mcp_services.json');

function readJSON(filePath) {
  return JSON.parse(fs.readFileSync(filePath, 'utf8'));
}

function slugify(name) {
  return name
    .toLowerCase()
    .replace(/&/g, ' and ')
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .replace(/--+/g, '-');
}

function ensureDir(dir) {
  fs.mkdirSync(dir, { recursive: true });
}

function formatYaml(obj, indent = 0) {
  const spaces = ' '.repeat(indent);
  if (Array.isArray(obj)) {
    if (obj.length === 0) return `${spaces}[]`;
    return obj
      .map((item) => {
        if (item && typeof item === 'object') {
          return `${spaces}- ${formatYaml(item, indent + 2).trimStart()}`;
        }
        return `${spaces}- ${item}`;
      })
      .join('\n');
  }
  if (obj && typeof obj === 'object') {
    const keys = Object.keys(obj);
    if (keys.length === 0) return `${spaces}{}`;
    return keys
      .map((key) => {
        const value = obj[key];
        if (value === undefined || value === null) return null;
        const child = formatYaml(value, indent + 2);
        if (typeof value === 'object' && value !== null && !Array.isArray(value)) {
          if (Object.keys(value).length === 0) {
            return `${spaces}${key}: {}`;
          }
          return `${spaces}${key}:\n${child}`;
        }
        if (Array.isArray(value)) {
          if (value.length === 0) {
            return `${spaces}${key}: []`;
          }
          return `${spaces}${key}:\n${child}`;
        }
        return `${spaces}${key}: ${child.trimStart()}`;
      })
      .filter(Boolean)
      .join('\n');
  }
  if (typeof obj === 'string') {
    if (obj.includes('\n')) {
      return `${spaces}|-\n${obj
        .split('\n')
        .map((line) => `${spaces}  ${line}`)
        .join('\n')}`;
    }
    if (/[:#\-\?%]/.test(obj)) {
      return `${spaces}"${obj.replace(/"/g, '\\"')}"`;
    }
    return `${spaces}${obj}`;
  }
  return `${spaces}${obj}`;
}

function normaliseRepoLink(link) {
  try {
    const url = new URL(link);
    const parts = url.pathname.split('/').filter(Boolean);
    if (parts[0] === 'mcp') {
      parts.shift();
    }
    if (parts.length >= 2) {
      return {
        repoPath: `${parts[0]}/${parts[1]}`,
        repoUrl: `https://github.com/${parts[0]}/${parts[1]}`,
        catalogUrl: link,
      };
    }
  } catch (err) {
    // ignore parse errors
  }
  return { repoPath: null, repoUrl: link, catalogUrl: link };
}

function findServiceForRepo(services, repoPath, fallbackName) {
  if (!repoPath) return null;
  const direct = services.find((svc) => {
    const refs = [svc.upstream, svc.source];
    return refs.some((ref) => typeof ref === 'string' && ref.includes(repoPath));
  });
  if (direct) return direct;
  if (fallbackName) {
    const slug = slugify(fallbackName);
    return (
      services.find((svc) => {
        const titleSlug = slugify(svc.title || '');
        return titleSlug === slug || titleSlug.startsWith(slug) || slug.startsWith(titleSlug);
      }) || null
    );
  }
  return null;
}

function normaliseImage(image) {
  if (!image) return null;
  if (/^https?:/.test(image)) return image;
  if (
    image.startsWith('mcr.microsoft.com') ||
    image.startsWith('ghcr.io') ||
    image.startsWith('docker.elastic.co')
  ) {
    return image;
  }
  if (image.startsWith('mcp/')) {
    return `docker.io/${image}`;
  }
  if (!image.includes('/')) {
    return `docker.io/${image}`;
  }
  return image;
}

function buildHeaders(remote) {
  if (!remote || !remote.headers) {
    return { staticHeaders: null, secretHeaders: null, secretEnv: [] };
  }
  const staticHeaders = {};
  const secretHeaders = {};
  const secretEnv = new Set();
  for (const [key, value] of Object.entries(remote.headers)) {
    if (typeof value !== 'string') {
      staticHeaders[key] = value;
      continue;
    }
    const match = value.match(/\$\{([^}]+)\}/);
    if (match) {
      const envName = match[1];
      secretHeaders[key] = `server:${envName}`;
      secretEnv.add(envName);
    } else {
      staticHeaders[key] = value;
    }
  }
  return {
    staticHeaders: Object.keys(staticHeaders).length ? staticHeaders : null,
    secretHeaders: Object.keys(secretHeaders).length ? secretHeaders : null,
    secretEnv: Array.from(secretEnv),
  };
}

function buildManifest(entry, service) {
  if (!service) return null;
  const slug = slugify(entry.name);
  const secretsSet = new Set();
  const notes = [];

  const secretVarsFromService = Array.isArray(service.secrets)
    ? service.secrets
        .map((s) => (s && (s.env || s.name) ? (s.env || s.name) : null))
        .filter(Boolean)
    : [];
  secretVarsFromService.forEach((env) => secretsSet.add(env));

  let transport = null;
  let withBlock = {};
  let requiresSecretBucket = false;

  if (service.remote && service.remote.url && service.remote.transport_type === 'streamable-http') {
    transport = 'streamable_http';
    const { staticHeaders, secretHeaders, secretEnv } = buildHeaders(service.remote);
    secretEnv.forEach((env) => secretsSet.add(env));
    if (secretEnv.length && !secretVarsFromService.length) {
      requiresSecretBucket = true;
    }
    withBlock = {
      transport,
      server: {
        baseURL: service.remote.url,
        ...(staticHeaders ? { headers: staticHeaders } : {}),
        ...(secretHeaders ? { headersFromSecret: secretHeaders } : {}),
      },
    };
  } else if (service.image || (Array.isArray(service.command) && service.command.length)) {
    transport = 'stdio';
    const image = normaliseImage(service.image);
    const commandList = Array.isArray(service.command) ? service.command : [];
    const onlyFlags = commandList.length > 0 && commandList.every((cmd) => typeof cmd === 'string' && cmd.trim().startsWith('--')) ;
    withBlock = {
      transport,
      stdio: {
        ...(image ? { image } : {}),
        imagePullPolicy: 'IfNotPresent',
        ...(commandList.length ? (onlyFlags ? { args: commandList } : { command: commandList }) : {}),
        ...(secretVarsFromService.length ? { useEphemeralSecret: true } : {}),
      },
    };
    if (secretVarsFromService.length) requiresSecretBucket = true;
  } else {
    return null;
  }

  const manifestName = `mcp-${slug}-${transport === 'streamable_http' ? 'http' : 'stdio'}`;
  const fileName = `${slug}-${transport === 'streamable_http' ? 'http' : 'stdio'}.yaml`;
  const secretsBlock =
    requiresSecretBucket || secretVarsFromService.length
      ? {
          server: `${slug}-mcp-secrets`,
        }
      : null;

  if (secretsSet.size) {
    notes.push(`Secrets: ${Array.from(secretsSet).join(', ')}`);
  }
  if (Array.isArray(service.volumes) && service.volumes.length) {
    notes.push('Requires volume mounts (not auto-configured).');
  }

  const manifest = {
    apiVersion: 'bubustack.io/v1alpha1',
    kind: 'Engram',
    metadata: {
      name: manifestName,
      namespace: 'default',
    },
    spec: {
      mode: 'job',
      templateRef: {
        name: 'mcp-adapter',
      },
      overrides: {
        serviceAccountName: 'mcp-adapter-sa',
      },
      with: withBlock,
      mcp: {
        initClientCapabilities: {},
      },
    },
  };

  if (secretsBlock) {
    manifest.spec.secrets = secretsBlock;
  }

  const yaml = formatYaml(manifest, 0);

  return {
    slug,
    transport,
    fileName,
    manifestPath: path.join(SERVERS_DIR, fileName),
    yaml,
    notes,
  };
}

function writeFileIfChanged(filePath, content) {
  const expected = `${content.trimEnd()}\n`;
  const existing = fs.existsSync(filePath) ? fs.readFileSync(filePath, 'utf8') : null;
  if (existing !== expected) {
    fs.writeFileSync(filePath, expected, 'utf8');
  }
}

function buildDoc(manifestRows, pendingRows) {
  const header = [
    '# MCP Adapter Engram Server Catalog',
    '',
    'This file is generated by `scripts/generate-catalog.js`.',
    'It merges the curated GitHub MCP index (`github-mcp-page`) with the Docker MCP catalog scrape (`mcp_services.json`).',
    '',
    '## Generated Engram manifests',
    '',
    '| Server | Transport | Example Engram manifest | GitHub repo | Docker MCP catalog | Notes |',
    '| - | - | - | - | - | - |',
  ];
  const rows = manifestRows.map((row) =>
    `| ${row.name} | \`${row.transport}\` | ${row.manifestLink} | ${row.githubLink} | ${row.catalogLink} | ${row.notes || '—'} |`,
  );
  const pendingHeader = [
    '',
    '## Pending entries',
    '',
    'Catalog items that do not yet have a Docker MCP catalog record (or require manual packaging).',
    '',
    '| Server | GitHub repo | Catalog link | Notes |',
    '| - | - | - | - |',
  ];
  const pending = pendingRows.map(
    (row) => `| ${row.name} | ${row.githubLink} | ${row.catalogLink} | ${row.notes || '—'} |`,
  );
  return [...header, ...rows, ...pendingHeader, ...pending, ''].join('\n');
}

function main() {
  ensureDir(SERVERS_DIR);
  const githubEntries = readJSON(GITHUB_PAGE_PATH);
  const services = readJSON(MCP_SERVICES_PATH);

  const manifestRows = [];
  const pendingRows = [];

  for (const entry of githubEntries) {
    const { repoPath, repoUrl, catalogUrl } = normaliseRepoLink(entry.link);
    const service = findServiceForRepo(services, repoPath, entry.name);
    const manifest = buildManifest(entry, service);

    if (manifest) {
      writeFileIfChanged(manifest.manifestPath, manifest.yaml);
      const manifestLink = `[examples/servers/${manifest.fileName}](../examples/servers/${manifest.fileName})`;
      const notes = manifest.notes.length ? manifest.notes.join(' ') : '—';
      manifestRows.push({
        name: entry.name,
        transport: manifest.transport,
        manifestLink,
        githubLink: repoUrl ? `[${repoPath}](${repoUrl})` : '—',
        catalogLink: service && service.readme ? `[Docker README](${service.readme})` : '—',
        notes,
      });
    } else {
      pendingRows.push({
        name: entry.name,
        githubLink: repoUrl ? `[${repoPath}](${repoUrl})` : '—',
        catalogLink: catalogUrl ? `[GitHub MCP catalog](${catalogUrl})` : '—',
        notes: service
          ? 'Missing remote URL and image metadata; manual packaging required.'
          : 'No Docker MCP catalog entry yet.',
      });
    }
  }

  manifestRows.sort((a, b) => a.name.localeCompare(b.name));
  pendingRows.sort((a, b) => a.name.localeCompare(b.name));

  const docContent = buildDoc(manifestRows, pendingRows);
  writeFileIfChanged(DOC_PATH, docContent);
}

main();
