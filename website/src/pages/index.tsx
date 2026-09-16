import Link from '@docusaurus/Link';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import Layout from '@theme/Layout';
import type { ReactNode } from 'react';

import styles from './index.module.css';

type Feature = {
  readonly title: string;
  readonly description: string;
};

type Capability = {
  readonly capability: string;
};

type Stat = {
  readonly label: string;
  readonly value: string;
  readonly unit: string;
  readonly context: string;
};

const FEATURES = [
  {
    title: 'Driver-layer interception',
    description:
      'Wraps database/sql at the driver, so you get a real *sql.DB back and every query is analyzed — including the ones your ORM writes',
  },
  {
    title: '21 detection rules',
    description:
      'SELECT *, DELETE/UPDATE without WHERE, leading wildcards, non-sargable predicates, cartesian joins, deep OFFSETs, and more',
  },
  {
    title: 'N+1 detection',
    description:
      'Flags the same query fingerprint repeating inside a window; scope it per request with ResetN1()',
  },
  {
    title: 'Slow-query detection',
    description:
      'Latency measured at the driver, reported against a threshold you set',
  },
  {
    title: 'Redaction by default',
    description:
      'Literals become ? before a finding leaves the process; every finding carries a PII-free fingerprint safe as a metric label',
  },
  {
    title: 'Quiet in production',
    description:
      'Per-finding de-duplication and an exact-query LRU cache — a hot query is parsed once and reported once per window',
  },
  {
    title: 'Static scanner',
    description:
      'sqlguard scan ./... walks Go source, resolves constants and fmt.Sprintf formats, and exits 1 for CI',
  },
  {
    title: 'EXPLAIN plan analyzer',
    description:
      'Plans a query against live Postgres, MySQL or MariaDB inside a rolled-back read-only transaction — never executes it',
  },
  {
    title: 'Six ORM integrations',
    description:
      'GORM, sqlx, native pgx/pgxpool, bun, xorm and ent, all built on the same Guard core with the same options',
  },
  {
    title: 'Opt-in real parsers',
    description:
      'Zero-dependency fallback by default; add pgparser or mysqlparser for AST-exact facts without touching the core',
  },
  {
    title: 'One YAML config',
    description:
      'A .sqlguard.yml drives the middleware, the scanner and the CLI — disable rules, override severities, tune thresholds',
  },
  {
    title: 'Inline suppressions',
    description:
      '-- sqlguard:ignore in SQL or // sqlguard:ignore in Go, per rule or wholesale, honored at runtime and statically',
  },
] as const satisfies readonly Feature[];

// Everything sqlguard provides that a hand-rolled query logger leaves to you.
// Kept in sync with the "Why sqlguard?" table in the repository README.
const CAPABILITIES = [
  { capability: 'Every query analyzed, ORM or raw, no wrapper type' },
  { capability: '21 SQL anti-pattern rules with tunable severities' },
  { capability: 'N+1 and slow-query detection at the driver' },
  { capability: 'Literal redaction + stable fingerprints' },
  { capability: 'De-duplication and per-query analysis cache' },
  { capability: 'Static scan of Go source for CI' },
  { capability: 'EXPLAIN plan analysis that never executes' },
  { capability: 'GORM / sqlx / pgx / bun / xorm / ent adapters' },
] as const satisfies readonly Capability[];

// Measured on an Intel i5-11400H @ 2.70GHz (12 cores), Go 1.27, Linux, with
// `go test -bench GuardCheck -benchmem ./middleware/`. Kept in sync with the
// Performance section of the repository README.
const STATS = [
  {
    label: 'Repeated query',
    value: '20',
    unit: 'ns/op',
    context: '0 allocs — analysis cache hit',
  },
  {
    label: 'First sighting',
    value: '22',
    unit: 'µs/op',
    context: 'full parse + every static rule',
  },
  {
    label: 'Cache speed-up',
    value: '~1000×',
    unit: '',
    context: 'default 1024-entry LRU',
  },
  {
    label: 'Core dependencies',
    value: '0',
    unit: 'third-party',
    context: 'analyzer, middleware, reporter',
  },
] as const satisfies readonly Stat[];

const INSTALL_COMMAND = 'go get github.com/KARTIKrocks/sqlguard';

const REPO_URL = 'https://github.com/KARTIKrocks/sqlguard';

function Hero(): ReactNode {
  return (
    <header className={styles.hero}>
      <div className="container">
        <h1 className={styles.title}>
          Production-safe SQL query analyzer for Go
        </h1>
        <p className={styles.subtitle}>
          Catch <code>SELECT *</code>, missing <code>WHERE</code> clauses, N+1
          loops and slow queries — at runtime through a{' '}
          <code>database/sql</code> driver wrapper, statically in CI, or from
          the query plan. Think <code>golangci-lint</code> for the SQL your
          application actually runs.
        </p>

        <div className={styles.buttons}>
          <Link
            className="button button--primary button--lg"
            to="/docs/getting-started">
            Get Started
          </Link>
          <Link
            className="button button--secondary button--lg"
            to="https://pkg.go.dev/github.com/KARTIKrocks/sqlguard">
            API Reference
          </Link>
        </div>

        <div className={styles.install}>
          <span className={styles.prompt} aria-hidden="true">
            $
          </span>
          <code>{INSTALL_COMMAND}</code>
        </div>
      </div>
    </header>
  );
}

function Features(): ReactNode {
  return (
    <section className="container" aria-label="Features">
      <div className={styles.features}>
        {FEATURES.map((feature) => (
          <article key={feature.title} className={styles.card}>
            <h2>{feature.title}</h2>
            <p>{feature.description}</p>
          </article>
        ))}
      </div>
    </section>
  );
}

function WhySqlguard(): ReactNode {
  return (
    <section className={styles.section}>
      <div className="container">
        <h2 className={styles.sectionTitle}>Why sqlguard?</h2>
        <p className={styles.sectionLead}>
          Query logging tells you what ran. It does not tell you that the query
          was a full-table <code>DELETE</code>, that the same lookup just ran
          400 times in a loop, or that a <code>LIKE '%…'</code> can never use
          the index. sqlguard sits at the one place every query passes through —
          the <code>database/sql</code> driver — and turns those into findings
          with a rule name, a redacted query and a fix.
        </p>

        <div className={styles.tableScroll}>
          <table className={styles.compare}>
            <thead>
              <tr>
                <th scope="col">Capability</th>
                <th scope="col">sqlguard</th>
                <th scope="col">Hand-rolled query logging</th>
              </tr>
            </thead>
            <tbody>
              {CAPABILITIES.map(({ capability }) => (
                <tr key={capability}>
                  <th scope="row">{capability}</th>
                  <td>
                    <span className={styles.check} aria-hidden="true">
                      ✓
                    </span>
                    <span className={styles.srOnly}>Included</span>
                  </td>
                  <td className={styles.diy}>You build it</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </section>
  );
}

function Performance(): ReactNode {
  return (
    <section className={styles.section}>
      <div className="container">
        <h2 className={styles.sectionTitle}>Built for the hot path</h2>
        <p className={styles.sectionLead}>
          Analysis runs on every intercepted query, so it has to be cheap. Rule
          configuration is resolved once at construction, repeated queries hit
          an exact-string LRU, and a hit is a map lookup with zero allocations.
          Measured on an Intel i5-11400H (12 cores), Go 1.27, Linux.
        </p>

        <div className={styles.stats}>
          {STATS.map((stat) => (
            <article key={stat.label} className={styles.stat}>
              <h3 className={styles.statLabel}>{stat.label}</h3>
              <p className={styles.statValue}>
                {stat.value}
                {stat.unit ? (
                  <span className={styles.statUnit}> {stat.unit}</span>
                ) : null}
              </p>
              <p className={styles.statContext}>{stat.context}</p>
            </article>
          ))}
        </div>

        <p className={styles.sectionNote}>
          Reproduce them yourself:{' '}
          <code>go test -bench GuardCheck -benchmem ./middleware/</code>. The
          repeated-query figure is <code>BenchmarkGuardCheck_Cached</code>, the
          first-sighting figure <code>BenchmarkGuardCheck_Uncached</code> (cache
          disabled). Source in{' '}
          <Link to={`${REPO_URL}/blob/main/middleware/cache_test.go`}>
            middleware/cache_test.go
          </Link>
          .
        </p>
      </div>
    </section>
  );
}

export default function Home(): ReactNode {
  const { siteConfig } = useDocusaurusContext();

  return (
    <Layout
      title={siteConfig.tagline}
      description="Detect slow queries, dangerous SQL patterns and N+1 loops in Go — at runtime through a database/sql driver wrapper, statically in CI, or from the EXPLAIN plan.">
      <Hero />
      <main>
        <Features />
        <WhySqlguard />
        <Performance />
      </main>
    </Layout>
  );
}
