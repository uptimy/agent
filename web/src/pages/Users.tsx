import { useState } from "react";
import { Navigate } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Copy, KeyRound, Plus, RefreshCw, ShieldOff, Trash2, Users as UsersIcon } from "lucide-react";
import { api, type Role, type User } from "@/lib/api";
import { timeAgo } from "@/lib/utils";
import { confirm } from "@/components/ui/confirm";
import { toast } from "@/components/ui/toast";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Field, Input, Select } from "@/components/ui/input";
import { ErrorNote, PageHeader } from "@/components/Layout";
import { useMe } from "@/components/AuthGate";

const roleLabels: Record<Role, { label: string; hint: string }> = {
  admin: { label: "Admin", hint: "Can change everything, including users" },
  viewer: { label: "Viewer", hint: "Can see monitors, history and settings, but not change them" },
};

/** Random temporary password, same shape as the server's generated ones. */
function tempPassword() {
  const bytes = crypto.getRandomValues(new Uint8Array(12));
  return btoa(String.fromCharCode(...bytes))
    .replace(/\+/g, "-")
    .replace(/\//g, "_");
}

type Credentials = { username: string; password: string; reason: "created" | "reset" };

export function Users() {
  const me = useMe();
  const users = useQuery({ queryKey: ["users"], queryFn: api.users, enabled: me.role === "admin" });
  const [adding, setAdding] = useState(false);
  const [shared, setShared] = useState<Credentials | null>(null);

  if (me.role !== "admin") return <Navigate to="/" replace />;

  return (
    <>
      <PageHeader
        title="Users"
        icon={UsersIcon}
        description="Everyone who can sign in to this agent."
        actions={
          !adding && (
            <Button
              onClick={() => {
                setAdding(true);
                setShared(null);
              }}
            >
              <Plus /> Add user
            </Button>
          )
        }
      />
      <div className="flex flex-col gap-4">
        {shared && <ShareCredentials creds={shared} onDone={() => setShared(null)} />}
        {adding && (
          <AddUserForm
            onCancel={() => setAdding(false)}
            onCreated={(c) => {
              setAdding(false);
              setShared(c);
            }}
          />
        )}
        <ErrorNote error={users.error} />
        {users.data && (
          <Card className="divide-y overflow-hidden">
            {users.data.map((u) => (
              <UserRow key={u.id} u={u} isMe={u.id === me.id} onReset={setShared} />
            ))}
          </Card>
        )}
      </div>
    </>
  );
}

function AddUserForm({ onCancel, onCreated }: { onCancel: () => void; onCreated: (c: Credentials) => void }) {
  const qc = useQueryClient();
  const [username, setUsername] = useState("");
  const [role, setRole] = useState<Role>("viewer");
  const [password, setPassword] = useState(tempPassword);
  const create = useMutation({
    mutationFn: () => api.createUser({ username, password, role }),
    onSuccess: (u) => {
      qc.invalidateQueries({ queryKey: ["users"] });
      onCreated({ username: u.username, password, reason: "created" });
    },
  });

  return (
    <Card section>
      <CardHeader>
        <CardTitle>Add user</CardTitle>
        <CardDescription>They'll sign in with a temporary password and choose their own.</CardDescription>
      </CardHeader>
      <CardContent>
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            create.mutate();
          }}
        >
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Username" htmlFor="u-name" hint="An email address works well.">
              <Input
                id="u-name"
                required
                autoFocus
                autoCapitalize="none"
                spellCheck={false}
                autoComplete="off"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                placeholder="dana@example.com"
              />
            </Field>
            <Field label="Role" htmlFor="u-role" hint={roleLabels[role].hint}>
              <Select id="u-role" value={role} onChange={(e) => setRole(e.target.value as Role)}>
                <option value="viewer">Viewer</option>
                <option value="admin">Admin</option>
              </Select>
            </Field>
          </div>
          <Field label="Temporary password" htmlFor="u-pass">
            <div className="flex gap-2">
              <Input
                id="u-pass"
                required
                minLength={8}
                autoComplete="off"
                className="font-mono"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
              <Button
                variant="outline"
                size="icon"
                aria-label="Generate another"
                title="Generate another"
                onClick={() => setPassword(tempPassword())}
              >
                <RefreshCw />
              </Button>
            </div>
          </Field>
          <ErrorNote error={create.error} />
          <div className="flex justify-end gap-2">
            <Button variant="ghost" onClick={onCancel}>
              Cancel
            </Button>
            <Button type="submit" disabled={create.isPending}>
              Add user
            </Button>
          </div>
        </form>
      </CardContent>
    </Card>
  );
}

/** Sign-in details to pass on, shown once after creating or resetting a user. */
function ShareCredentials({ creds, onDone }: { creds: Credentials; onDone: () => void }) {
  const [copied, setCopied] = useState(false);
  const text = `Sign in to Uptimy Agent\n${window.location.origin}\nUsername: ${creds.username}\nTemporary password: ${creds.password}\nYou'll be asked to choose your own password.`;
  return (
    <Card section className="border-up/40 bg-up/5">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Check className="size-4 text-up" />
          {creds.reason === "created" ? `${creds.username} can now sign in` : `Password reset for ${creds.username}`}
        </CardTitle>
        <CardDescription>Share these details with them. The temporary password won't be shown again.</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <pre className="overflow-x-auto rounded-md border bg-card p-3 text-sm whitespace-pre-wrap">{text}</pre>
        <div className="flex gap-2">
          <Button
            onClick={() => {
              navigator.clipboard.writeText(text);
              setCopied(true);
            }}
          >
            {copied ? <Check /> : <Copy />} {copied ? "Copied" : "Copy details"}
          </Button>
          <Button variant="ghost" onClick={onDone}>
            Done
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

function UserRow({ u, isMe, onReset }: { u: User; isMe: boolean; onReset: (c: Credentials) => void }) {
  const qc = useQueryClient();
  const refresh = () => qc.invalidateQueries({ queryKey: ["users"] });
  const setRole = useMutation({ mutationFn: (role: Role) => api.updateUser(u.id, { role }), onSuccess: refresh });
  const reset = useMutation({
    mutationFn: async () => {
      const password = tempPassword();
      await api.updateUser(u.id, { password });
      return password;
    },
    onSuccess: (password) => {
      refresh();
      onReset({ username: u.username, password, reason: "reset" });
    },
  });
  const resetTwoFactor = useMutation({
    mutationFn: () => api.resetTwoFactor(u.id),
    onSuccess: () => {
      refresh();
      toast.success("Two-factor sign-in turned off", u.username);
    },
  });
  const remove = useMutation({
    mutationFn: () => api.deleteUser(u.id),
    onSuccess: () => {
      refresh();
      toast.success("User deleted", u.username);
    },
  });
  const locked = isMe || u.password_managed_by_env;

  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-3 px-5 py-4">
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          <span className="truncate font-medium">{u.username}</span>
          {isMe && <Badge>You</Badge>}
          {u.password_managed_by_env && <Badge title="Defined by ADMIN_USERNAME / ADMIN_PASSWORD">Env</Badge>}
          {u.must_change_password && <Badge className="border-paused/40 text-paused">Hasn't signed in yet</Badge>}
          {u.two_factor && <Badge title="Signs in with a code from an authenticator app">Two-factor</Badge>}
        </div>
        <div className="mt-0.5 text-xs text-muted-foreground">
          {u.last_login_at ? `Last signed in ${timeAgo(u.last_login_at)}` : "Never signed in"}
        </div>
      </div>

      {locked ? (
        <span className="w-28 text-sm text-muted-foreground">{roleLabels[u.role].label}</span>
      ) : (
        <Select
          aria-label={`Role for ${u.username}`}
          className="h-8 w-28"
          value={u.role}
          disabled={setRole.isPending}
          onChange={(e) => setRole.mutate(e.target.value as Role)}
        >
          <option value="admin">Admin</option>
          <option value="viewer">Viewer</option>
        </Select>
      )}

      <div className="flex gap-1">
        {!locked && (
          <>
            <Button
              variant="ghost"
              size="icon"
              aria-label={`Reset password for ${u.username}`}
              title="Reset password"
              disabled={reset.isPending}
              onClick={() =>
                confirm({
                  title: "Reset password",
                  subtitle: `${u.username} will be signed out`,
                  tone: "default",
                  icon: KeyRound,
                  message: (
                    <>
                      <strong className="text-foreground">{u.username}</strong> gets a new temporary password, which
                      you'll see next to pass on. They choose their own at the next sign-in.
                    </>
                  ),
                  confirmLabel: "Reset password",
                  action: () => reset.mutateAsync(),
                })
              }
            >
              <KeyRound />
            </Button>
            {u.two_factor && (
              <Button
                variant="ghost"
                size="icon"
                aria-label={`Turn off two-factor sign-in for ${u.username}`}
                title="Turn off two-factor sign-in"
                disabled={resetTwoFactor.isPending}
                onClick={() =>
                  confirm({
                    title: "Turn off two-factor sign-in",
                    subtitle: "For a user who lost their phone and recovery codes",
                    tone: "default",
                    icon: ShieldOff,
                    message: (
                      <>
                        <strong className="text-foreground">{u.username}</strong> signs in with their password alone
                        until they set it up again from their account page.
                      </>
                    ),
                    confirmLabel: "Turn off",
                    action: () => resetTwoFactor.mutateAsync(),
                  })
                }
              >
                <ShieldOff />
              </Button>
            )}
            <Button
              variant="ghost"
              size="icon"
              aria-label={`Delete ${u.username}`}
              title="Delete user"
              disabled={remove.isPending}
              onClick={() =>
                confirm({
                  title: "Delete user",
                  message: (
                    <>
                      <strong className="text-foreground">{u.username}</strong> is signed out immediately and can't sign
                      in again.
                    </>
                  ),
                  confirmLabel: "Delete user",
                  action: () => remove.mutateAsync(),
                })
              }
            >
              <Trash2 />
            </Button>
          </>
        )}
      </div>
      {setRole.error && (
        <div className="basis-full">
          <ErrorNote error={setRole.error} />
        </div>
      )}
    </div>
  );
}
