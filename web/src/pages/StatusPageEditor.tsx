import { useMemo, useState } from "react";
import { cn } from "@/lib/utils";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Award,
  ExternalLink,
  Eye,
  FileText,
  Globe,
  Megaphone,
  Palette,
  Pencil,
  RotateCcw,
  RotateCw,
  Settings2,
} from "lucide-react";
import { useSearchParams } from "react-router";
import { CopyButton } from "@/components/ui/copy-button";
import { api, type StatusPageConfig, type StatusPageLogos } from "@/lib/api";
import { Button, buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Field, Input, Select, Switch, Textarea } from "@/components/ui/input";
import { toast } from "@/components/ui/toast";
import { ErrorNote, PageHeader } from "@/components/Layout";
import { useCanEdit } from "@/components/AuthGate";
import { LogoField } from "@/components/status-page/LogoField";
import { AnnouncementEditor } from "@/components/status-page/AnnouncementEditor";
import { DEFAULT_ACCENT, SectionsEditor } from "@/components/status-page/SectionsEditor";

type Draft = StatusPageConfig;

export function StatusPageEditor() {
  const qc = useQueryClient();
  const canEdit = useCanEdit();
  const config = useQuery({ queryKey: ["status-page"], queryFn: api.statusPageConfig });
  // Only edits are kept in state; without any, the page shows the server copy,
  // so it follows changes made elsewhere without ever discarding unsaved work.
  const [edits, setEdits] = useState<Draft | null>(null);
  const draft = edits ?? config.data ?? null;
  const [previewKey, setPreviewKey] = useState(0);
  const dirty = useMemo(
    () => edits !== null && JSON.stringify(edits) !== JSON.stringify(config.data),
    [edits, config.data],
  );
  const edit = (update: (d: Draft) => Draft) => setEdits((d) => update(d ?? config.data!));

  const save = useMutation({
    mutationFn: () => {
      const d = draft!;
      return api.saveStatusPageConfig({
        enabled: d.enabled,
        title: d.title,
        description: d.description,
        show_events: d.show_events,
        history_days: d.history_days,
        accent_color: d.accent_color,
        website_url: d.website_url,
        domain: d.domain,
        sections: d.sections,
        monitors: d.monitors.map(({ id, public: pub, label, section }) => ({ id, public: pub, label, section })),
      });
    },
    onSuccess: (saved) => {
      qc.setQueryData(["status-page"], saved);
      setEdits(null);
      qc.invalidateQueries({ queryKey: ["info"] });
      setPreviewKey((k) => k + 1);
      toast.success("Status page saved");
    },
  });

  // Logos save right away, without touching other unsaved edits.
  const applyLogos = (logos: StatusPageLogos) => {
    qc.setQueryData<StatusPageConfig>(["status-page"], (c) => c && { ...c, logos });
    setEdits((d) => d && { ...d, logos });
    setPreviewKey((k) => k + 1);
  };

  const set = <K extends keyof Draft>(key: K, value: Draft[K]) => edit((d) => ({ ...d, [key]: value }));
  const domain = config.data?.domain;
  const publicURL = domain ? `https://${domain}` : "/status";
  const domainClash = !!draft && isDashboardHost(draft.domain);

  const [params, setParams] = useSearchParams();
  const tab = (TABS.find((t) => t.id === params.get("tab"))?.id ?? "editor") as TabID;
  const setTab = (t: TabID) => setParams(t === "editor" ? {} : { tab: t }, { replace: true });
  const pageURL = domain ? `https://${domain}/` : `${window.location.origin}/status`;

  return (
    <>
      <PageHeader
        title="Status page"
        icon={FileText}
        description="What visitors see. Only names and uptime are shown, never hostnames or URLs."
        actions={
          <a href={publicURL} target="_blank" rel="noreferrer" className={buttonVariants({ variant: "outline" })}>
            View <ExternalLink />
          </a>
        }
      />
      <ErrorNote error={config.error} />
      {draft && (
        <div className="flex flex-col gap-6">
          <nav
            aria-label="Status page settings"
            className="flex gap-1 overflow-x-auto rounded-xl border bg-card p-1.5 shadow-xs sm:gap-2"
          >
            {TABS.map((t) => (
              <button
                key={t.id}
                type="button"
                aria-current={tab === t.id ? "page" : undefined}
                onClick={() => setTab(t.id)}
                className={cn(
                  "flex shrink-0 items-center gap-2 rounded-lg border border-transparent px-3 py-2 text-sm font-medium whitespace-nowrap transition-colors sm:px-4",
                  tab === t.id
                    ? "border-primary/40 bg-primary/10 text-primary"
                    : "text-muted-foreground hover:bg-muted hover:text-foreground",
                )}
              >
                <t.icon className="size-4" />
                <span className={cn(tab !== t.id && "hidden sm:inline")}>{t.label}</span>
              </button>
            ))}
          </nav>

          {!canEdit && (
            <p className="rounded-md border bg-muted/50 px-3 py-2 text-sm text-muted-foreground">
              You have read-only access. Ask an admin to change the status page.
            </p>
          )}

          {(tab === "editor" || tab === "domain") && (
            <form
              onSubmit={(e) => {
                e.preventDefault();
                save.mutate();
              }}
            >
              <fieldset disabled={!canEdit} className="flex flex-col gap-6">
                {tab === "editor" ? (
                  <>
                    <Card>
                      <CardHeader>
                        <CardTitle className="flex items-center gap-2">
                          <Settings2 className="size-4" /> Page settings
                        </CardTitle>
                        <CardDescription>Whether the page is published, and what it shows.</CardDescription>
                      </CardHeader>
                      <CardContent className="flex flex-col gap-5">
                        <div className="flex flex-col gap-1.5">
                          <span className="text-sm font-medium">Page URL</span>
                          <div className="flex items-center gap-2 rounded-md border bg-muted/50 py-1 pr-1 pl-3">
                            <span className="min-w-0 flex-1 truncate font-mono text-sm">{pageURL}</span>
                            <CopyButton text={pageURL} label="Copy URL" />
                            <a
                              href={publicURL}
                              target="_blank"
                              rel="noreferrer"
                              className={buttonVariants({ variant: "outline", size: "sm" })}
                            >
                              View
                            </a>
                          </div>
                          <p className="text-xs text-muted-foreground">
                            {domain ? (
                              "Its own domain. Change it under Custom domain."
                            ) : (
                              <>
                                Give it its own address under{" "}
                                <button type="button" className="underline" onClick={() => setTab("domain")}>
                                  Custom domain
                                </button>
                                .
                              </>
                            )}
                          </p>
                        </div>
                        <SettingRow
                          title="Published"
                          description={
                            draft.enabled
                              ? "Anyone with the link can see the page."
                              : "Visitors get a “not found” page."
                          }
                        >
                          <Switch
                            id="sp-enabled"
                            checked={draft.enabled}
                            onChange={(v) => set("enabled", v)}
                            label={<span className="sr-only">Published</span>}
                          />
                        </SettingRow>
                        <SettingRow
                          title="Recent events"
                          description="List outages and recoveries at the bottom of the page."
                        >
                          <Switch
                            id="sp-events"
                            checked={draft.show_events}
                            onChange={(v) => set("show_events", v)}
                            label={<span className="sr-only">Recent events</span>}
                          />
                        </SettingRow>
                        <SettingRow
                          title="Days of history"
                          description="Daily bars per monitor, and the period of its uptime. Phones show 30, tablets up to 60."
                        >
                          <Select
                            aria-label="Days of history"
                            className="w-28"
                            value={draft.history_days}
                            onChange={(e) => set("history_days", Number(e.target.value) as 30 | 60 | 90)}
                          >
                            {[30, 60, 90].map((n) => (
                              <option key={n} value={n}>
                                {n} days
                              </option>
                            ))}
                          </Select>
                        </SettingRow>
                      </CardContent>
                    </Card>

                    <Card>
                      <CardHeader>
                        <CardTitle className="flex items-center gap-2">
                          <Palette className="size-4" /> Branding
                        </CardTitle>
                        <CardDescription>The page&apos;s name, website link, logos and accent color.</CardDescription>
                      </CardHeader>
                      <CardContent className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.6fr)]">
                        <div className="flex flex-col gap-5">
                          <Field
                            label="Page name"
                            htmlFor="sp-title"
                            hint="Shown when there's no logo, and as the browser tab title."
                          >
                            <Input
                              id="sp-title"
                              required
                              maxLength={100}
                              value={draft.title}
                              onChange={(e) => set("title", e.target.value)}
                            />
                          </Field>
                          <Field
                            label="Website URL"
                            htmlFor="sp-website"
                            hint='Adds a "Visit website" link to the header.'
                          >
                            <Input
                              id="sp-website"
                              type="url"
                              placeholder="https://example.com"
                              maxLength={300}
                              value={draft.website_url}
                              onChange={(e) => set("website_url", e.target.value)}
                            />
                          </Field>
                          <Field
                            label="Description"
                            htmlFor="sp-desc"
                            hint="Optional. Shown under the name, e.g. who to contact during an outage."
                          >
                            <Textarea
                              id="sp-desc"
                              maxLength={500}
                              rows={3}
                              value={draft.description}
                              onChange={(e) => set("description", e.target.value)}
                            />
                          </Field>
                        </div>
                        <div className="flex flex-col gap-5">
                          <div className="grid gap-4 sm:grid-cols-2">
                            <LogoField
                              variant="light"
                              label="Logo"
                              hint="Replaces the name. PNG, SVG, JPEG or WebP, up to 512 KB."
                              url={draft.logos.light}
                              onChange={applyLogos}
                              disabled={!canEdit}
                            />
                            <LogoField
                              variant="dark"
                              label="Logo for dark mode"
                              hint="Optional. Otherwise the logo above is used."
                              url={draft.logos.dark}
                              onChange={applyLogos}
                              disabled={!canEdit}
                            />
                          </div>
                          <Field label="Accent color" htmlFor="sp-accent" hint="Used for section headings.">
                            <div className="flex items-center gap-2">
                              <input
                                type="color"
                                aria-label="Pick accent color"
                                value={draft.accent_color || DEFAULT_ACCENT}
                                onChange={(e) => set("accent_color", e.target.value)}
                                className="h-9 w-12 shrink-0 cursor-pointer rounded-md border bg-card p-1"
                              />
                              <Input
                                id="sp-accent"
                                className="w-32 font-mono"
                                placeholder={DEFAULT_ACCENT}
                                maxLength={7}
                                value={draft.accent_color}
                                onChange={(e) => set("accent_color", e.target.value)}
                              />
                              {draft.accent_color && (
                                <Button variant="ghost" size="sm" onClick={() => set("accent_color", "")}>
                                  <RotateCcw /> Default
                                </Button>
                              )}
                            </div>
                          </Field>
                        </div>
                      </CardContent>
                    </Card>

                    <SectionsEditor draft={draft} onChange={edit} />
                  </>
                ) : (
                  <DomainCard value={draft.domain} onChange={(v) => set("domain", v)} />
                )}

                {canEdit && (
                  <div className="sticky bottom-4 z-10 flex items-center justify-end gap-3 rounded-lg border bg-card/95 px-4 py-3 shadow-sm backdrop-blur">
                    <ErrorNote error={save.error} />
                    <span className="mr-auto text-sm text-muted-foreground">
                      {dirty ? "Unsaved changes" : "Up to date"}
                    </span>
                    <Button variant="ghost" disabled={!dirty || save.isPending} onClick={() => setEdits(null)}>
                      Discard
                    </Button>
                    <Button type="submit" disabled={!dirty || save.isPending || domainClash}>
                      {save.isPending ? "Saving…" : "Save changes"}
                    </Button>
                  </div>
                )}
              </fieldset>
            </form>
          )}

          {tab === "announcement" && (
            <AnnouncementEditor canEdit={canEdit} onChange={() => setPreviewKey((k) => k + 1)} />
          )}

          {tab === "badges" &&
            (config.data?.enabled ? (
              <BadgesCard config={config.data} base={domain ? `https://${domain}` : window.location.origin} />
            ) : (
              <Card className="px-6 py-12 text-center text-sm text-muted-foreground">
                Badges are served while the status page is published.
              </Card>
            ))}

          {tab === "preview" && (
            <Card className="overflow-hidden">
              <div className="flex items-center justify-between border-b px-4 py-2.5">
                <span className="text-sm font-medium">Preview</span>
                <span className="flex items-center gap-2 text-xs text-muted-foreground">
                  {dirty ? "Shows the saved page; save to see your changes" : "Live"}
                  <button
                    type="button"
                    aria-label="Reload preview"
                    className="rounded p-1 hover:bg-muted hover:text-foreground"
                    onClick={() => setPreviewKey((k) => k + 1)}
                  >
                    <RotateCw className="size-3.5" />
                  </button>
                </span>
              </div>
              {config.data?.enabled ? (
                <iframe
                  key={previewKey}
                  src="/status?preview"
                  title="Status page preview"
                  className="h-[75vh] w-full bg-background"
                />
              ) : (
                <p className="px-4 py-16 text-center text-sm text-muted-foreground">
                  The status page isn&apos;t published. Visitors get a “not found” page.
                </p>
              )}
            </Card>
          )}
        </div>
      )}
    </>
  );
}

type TabID = "editor" | "announcement" | "domain" | "badges" | "preview";

/** The editor's tabs, in the hosted Uptimy app's order. */
const TABS: { id: TabID; label: string; icon: typeof Pencil }[] = [
  { id: "editor", label: "Editor", icon: Pencil },
  { id: "announcement", label: "Announcement", icon: Megaphone },
  { id: "domain", label: "Custom domain", icon: Globe },
  { id: "badges", label: "Badges", icon: Award },
  { id: "preview", label: "Preview", icon: Eye },
];

/** A setting with its explanation on the left and its control on the right. */
function SettingRow({
  title,
  description,
  children,
}: {
  title: string;
  description: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex items-center justify-between gap-4 rounded-lg bg-muted/40 px-4 py-3">
      <div className="min-w-0">
        <div className="text-sm font-medium">{title}</div>
        <div className="text-xs text-muted-foreground">{description}</div>
      </div>
      {children}
    </div>
  );
}

// hostOf turns a pasted address into its hostname, as the server does.
function hostOf(v: string) {
  return v
    .trim()
    .toLowerCase()
    .replace(/^[a-z]+:\/\//, "")
    .replace(/[/?#].*$/, "")
    .replace(/:\d+$/, "")
    .replace(/\.$/, "");
}

// isDashboardHost reports whether the domain is the address in use, which
// would then show only the status page.
function isDashboardHost(domain: string) {
  const host = hostOf(domain);
  return host !== "" && host === window.location.hostname;
}

function DomainCard({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const isDashboard = isDashboardHost(value);
  return (
    <Card>
      <CardHeader>
        <CardTitle>Custom domain</CardTitle>
        <CardDescription>
          Optional. A separate address, like status.example.com, that shows only the status page, at its root. Sign-in,
          the dashboard and the API aren't served there, so they can stay private.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <Field
          label="Domain"
          htmlFor="sp-domain"
          hint="Point its DNS at the agent and add TLS where you expose it: an Ingress, Railway's custom domains, or a proxy like Caddy. Heartbeat ping URLs work on it too."
        >
          <div className="relative">
            <Globe className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              id="sp-domain"
              className="pl-9 font-mono"
              placeholder="status.example.com"
              maxLength={253}
              autoComplete="off"
              spellCheck={false}
              value={value}
              onChange={(e) => onChange(e.target.value)}
              aria-invalid={isDashboard || undefined}
            />
          </div>
        </Field>
        {isDashboard && (
          <p className="rounded-md border border-down/30 bg-down/10 px-3 py-2 text-sm text-down">
            That's the address you're using right now. Use a separate one for the status page, or this address would
            show only the status page and you'd lose the dashboard here.
          </p>
        )}
      </CardContent>
    </Card>
  );
}

type BadgeType = "status" | "24h" | "7d" | "30d";

/** Badges for READMEs: the page's overall status and each monitor on it. */
function BadgesCard({ config, base }: { config: StatusPageConfig; base: string }) {
  const [type, setType] = useState<BadgeType>("status");
  // On its own domain the page is at the root.
  const page = base === window.location.origin ? `${base}/status` : `${base}/`;
  const monitors = config.monitors.filter((m) => m.public);
  const url = (id: number) =>
    type === "status" ? `${base}/badge/${id}/status.svg` : `${base}/badge/${id}/uptime.svg?period=${type}`;
  const rows = [
    { key: "overall", name: config.title, src: `${base}/badge/status.svg` },
    ...monitors.map((m) => ({ key: String(m.id), name: m.label || m.name, src: url(m.id) })),
  ];
  return (
    <Card>
      <CardHeader>
        <CardTitle>Badges</CardTitle>
        <CardDescription>
          For a README or wiki. They link to the status page and exist only for monitors on it.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <div className="flex w-fit gap-1 rounded-md bg-muted p-0.5" role="radiogroup" aria-label="Badge type">
          {(
            [
              ["status", "Status"],
              ["24h", "Uptime 24h"],
              ["7d", "7 days"],
              ["30d", "30 days"],
            ] as const
          ).map(([t, label]) => (
            <button
              key={t}
              type="button"
              role="radio"
              aria-checked={type === t}
              onClick={() => setType(t)}
              className={cn(
                "rounded px-3 py-1.5 text-sm font-medium",
                type === t ? "bg-card shadow-xs" : "text-muted-foreground",
              )}
            >
              {label}
            </button>
          ))}
        </div>
        <ul className="divide-y rounded-lg border">
          {rows.map((r, i) => {
            const markdown = `[![${r.name}](${r.src})](${page})`;
            return (
              <li key={r.key} className="flex items-center gap-3 px-3 py-2">
                <span className="min-w-0 flex-1 truncate text-sm">
                  {r.name}
                  {i === 0 && <span className="text-muted-foreground"> · overall</span>}
                </span>
                {/* Served from this agent, so the preview works before DNS does. */}
                <img src={r.src.replace(base, "")} alt="" className="h-5" />
                <CopyButton text={markdown} label="Copy Markdown" />
              </li>
            );
          })}
        </ul>
        {monitors.length === 0 && (
          <p className="text-sm text-muted-foreground">Add monitors to the page to get a badge for each.</p>
        )}
      </CardContent>
    </Card>
  );
}
