import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { KeyRound, Plus } from "lucide-react";
import { api } from "@/lib/api";
import { formatDateTime, timeAgo } from "@/lib/utils";
import { confirm } from "@/components/ui/confirm";
import { toast } from "@/components/ui/toast";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { CopyField } from "@/components/ui/copy-button";
import { Field, Input, Switch } from "@/components/ui/input";
import { ErrorNote } from "@/components/Layout";
import { useMe } from "@/components/AuthGate";

/** Personal API tokens: create, see once, revoke. */
export function APITokensCard() {
  const me = useMe();
  const qc = useQueryClient();
  const tokens = useQuery({ queryKey: ["api-tokens"], queryFn: api.apiTokens });
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [readOnly, setReadOnly] = useState(me.role !== "admin");
  const [created, setCreated] = useState<{ name: string; token: string } | null>(null);

  const create = useMutation({
    mutationFn: () => api.createAPIToken({ name, read_only: readOnly }),
    onSuccess: (res) => {
      qc.invalidateQueries({ queryKey: ["api-tokens"] });
      setCreated({ name: res.api_token.name, token: res.token });
      setCreating(false);
      setName("");
    },
  });
  const revoke = useMutation({
    mutationFn: (id: number) => api.deleteAPIToken(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["api-tokens"] });
      toast.success("Token revoked", "Anything using it stops working right away.");
    },
  });
  const origin = window.location.origin;

  return (
    <Card section>
      <CardHeader className="flex-row items-start justify-between gap-4">
        <div>
          <CardTitle>API tokens</CardTitle>
          <CardDescription>
            For scripts, CI and tools like Terraform. A token acts as you; read-only ones can&apos;t change anything.
            Tokens can&apos;t manage accounts or other tokens.
          </CardDescription>
        </div>
        {!creating && (
          <Button variant="outline" size="sm" onClick={() => setCreating(true)}>
            <Plus /> New token
          </Button>
        )}
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {created && (
          <div className="flex flex-col gap-2 rounded-lg border border-primary/40 bg-primary/5 p-4">
            <p className="text-sm font-medium">Copy “{created.name}” now. It won&apos;t be shown again.</p>
            <CopyField text={created.token} label="Copy token" />
            <p className="text-xs text-muted-foreground">Use it as a bearer token, for example:</p>
            <CopyField text={`curl -H "Authorization: Bearer ${created.token}" ${origin}/api/healthchecks`} />
            <div>
              <Button variant="ghost" size="sm" onClick={() => setCreated(null)}>
                Done
              </Button>
            </div>
          </div>
        )}

        {creating && (
          <form
            className="flex flex-col gap-3 border-t pt-4"
            onSubmit={(e) => {
              e.preventDefault();
              create.mutate();
            }}
          >
            <Field label="Name" htmlFor="t-name" hint="What uses it, so you know what breaks if you revoke it.">
              <Input
                id="t-name"
                required
                autoFocus
                maxLength={60}
                value={name}
                placeholder="GitHub Actions"
                onChange={(e) => setName(e.target.value)}
              />
            </Field>
            {me.role === "admin" ? (
              <Switch id="t-read-only" checked={readOnly} onChange={setReadOnly} label="Read-only" />
            ) : (
              <p className="text-xs text-muted-foreground">Your tokens are read-only, like your account.</p>
            )}
            <ErrorNote error={create.error} />
            <div className="flex justify-end gap-2">
              <Button type="button" variant="ghost" onClick={() => setCreating(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={create.isPending}>
                Create token
              </Button>
            </div>
          </form>
        )}

        <ErrorNote error={tokens.error} />
        {tokens.data?.length ? (
          <ul className="divide-y">
            {tokens.data.map((t) => (
              <li key={t.id} className="flex items-center gap-3 py-3 text-sm">
                <KeyRound className="size-4 shrink-0 text-muted-foreground" />
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-medium">{t.name}</span>
                    <code className="text-xs text-muted-foreground">upa_…{t.hint}</code>
                    {t.read_only && <Badge>Read-only</Badge>}
                  </div>
                  <div className="text-xs text-muted-foreground">
                    Created {formatDateTime(t.created_at)} ·{" "}
                    {t.last_used_at ? `last used ${timeAgo(t.last_used_at)}` : "never used"}
                  </div>
                </div>
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() =>
                    confirm({
                      title: "Revoke token",
                      message: (
                        <>
                          Anything using <strong className="text-foreground">{t.name}</strong> stops working right away.
                        </>
                      ),
                      confirmLabel: "Revoke",
                      action: () => revoke.mutateAsync(t.id),
                    })
                  }
                >
                  Revoke
                </Button>
              </li>
            ))}
          </ul>
        ) : (
          tokens.isSuccess && !creating && <p className="text-sm text-muted-foreground">No tokens yet.</p>
        )}
      </CardContent>
    </Card>
  );
}
