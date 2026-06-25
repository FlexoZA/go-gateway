import Link from "next/link";
import { Callout, CodeBlock, Endpoint } from "@/components/docs/doc-kit";
import { buildBuiltInCollections } from "@/lib/console/default-collections";
import type { ConsoleRequest } from "@/lib/console/types";

export const metadata = { title: "HTTP API reference · Docs" };

function EndpointRow({ req }: { req: ConsoleRequest }) {
  const params = req.params.map((p) => p.key).filter(Boolean);
  return (
    <div className="mb-1">
      <Endpoint method={req.method} path={req.path}>
        {req.description}
      </Endpoint>
      {params.length > 0 && (
        <p className="mt-1 text-xs text-slate-500">
          Query params: {params.map((p) => <code key={p}>{p}</code>).reduce((a, b) => (
            <>{a}, {b}</>
          ))}
        </p>
      )}
      {req.body.mode !== "none" && req.body.text.trim() && (
        <CodeBlock label="Example body">{req.body.text}</CodeBlock>
      )}
    </div>
  );
}

export default function ApiReferencePage() {
  const groups = buildBuiltInCollections();
  return (
    <article className="doc-prose">
      <h1 className="text-2xl font-semibold text-white">HTTP API reference</h1>
      <p>
        The gateway exposes a management/control HTTP API (default port <code>8080</code>; serve it
        behind TLS in production). All responses are JSON. This reference is generated from the same
        endpoint catalog the <Link href="/api-console">API Console</Link> uses, so it always matches
        what the Console can send.
      </p>

      <h2 id="auth">Authentication</h2>
      <p>
        <code>GET /healthz</code> is public. Every route under <code>/api/</code> requires an API key:
      </p>
      <CodeBlock label="Header">{`Authorization: Bearer dgw_<key>`}</CodeBlock>
      <p>
        Mint a key on the <Link href="/api-keys">API Keys</Link> page (shown once). Common status
        codes:
      </p>
      <ul>
        <li>
          <code>401</code> — missing, malformed, unknown, revoked, or expired key.
        </li>
        <li>
          <code>503</code> — the key store / database isn&rsquo;t configured.
        </li>
        <li>
          <code>404</code> — unit not connected / resource absent; <code>409</code> — device asleep
          (<code>device_sleeping</code>) or clip not ready; <code>502</code>/<code>504</code> — device
          rejected or didn&rsquo;t answer a command in time.
        </li>
      </ul>
      <Callout tone="info">
        This reference shows each endpoint&rsquo;s method, path, and (where relevant) an example
        request body. To see live responses, send the request from the{" "}
        <Link href="/api-console">API Console</Link>. For the Howen read-flow with response examples,
        see the <Link href="/docs/howen">Howen guide</Link>.
      </Callout>

      {groups.map((group) => (
        <section key={group.id}>
          <h2 id={group.id}>{group.name}</h2>
          {group.description && <p>{group.description}</p>}
          {group.requests.map((req) => (
            <EndpointRow key={req.id} req={req} />
          ))}
        </section>
      ))}
    </article>
  );
}
