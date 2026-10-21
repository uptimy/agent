import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { LogOut, UserRound } from "lucide-react";
import { api, type AuthState } from "@/lib/api";
import { formatDateTime, timeAgo } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Field, PasswordInput } from "@/components/ui/input";
import { ErrorNote, PageHeader } from "@/components/Layout";
import { useMe, useSignOut } from "@/components/AuthGate";
import { Avatar } from "@/components/UserMenu";
import { APITokensCard } from "@/components/APITokens";
import { TwoFactorCard } from "@/components/TwoFactor";

/** The signed-in user's own account: who they are, password, devices, API tokens. */
export function Account() {
  const me = useMe();
  const signOut = useSignOut();

  return (
    <div className="mx-auto max-w-2xl">
      <PageHeader title="Account" icon={UserRound} />
      <div className="flex flex-col gap-6">
        <Card>
          <CardContent className="flex items-center gap-4 pt-5">
            <Avatar name={me.username} className="size-12 text-lg" />
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-2">
                <span className="truncate text-lg font-semibold">{me.username}</span>
                <Badge>{me.role === "admin" ? "Admin" : "Viewer"}</Badge>
              </div>
              <p className="mt-0.5 text-sm text-muted-foreground">
                {me.role === "admin"
                  ? "Can change everything on this agent, including users."
                  : "Can see monitors, history and settings, but not change them."}{" "}
                Member since {formatDateTime(me.created_at)}.
              </p>
            </div>
          </CardContent>
        </Card>

        <PasswordCard />
        <TwoFactorCard />
        <DevicesCard />
        <APITokensCard />

        <div>
          <Button variant="outline" onClick={() => signOut.mutate()} disabled={signOut.isPending}>
            <LogOut /> Sign out
          </Button>
        </div>
      </div>
    </div>
  );
}

function PasswordCard() {
  const me = useMe();
  const qc = useQueryClient();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const change = useMutation({
    mutationFn: async () => {
      if (next !== confirm) throw new Error("The new passwords don't match");
      return api.changePassword(current, next);
    },
    onSuccess: (s: AuthState) => {
      qc.setQueryData(["auth"], s);
      qc.invalidateQueries({ queryKey: ["sessions"] });
      setCurrent("");
      setNext("");
      setConfirm("");
    },
  });

  if (me.password_managed_by_env) {
    return (
      <Card section>
        <CardHeader>
          <CardTitle>Password</CardTitle>
          <CardDescription>
            This account's password is set by the <code className="rounded bg-muted px-1">ADMIN_PASSWORD</code>{" "}
            environment variable. Change it there and restart the agent.
          </CardDescription>
        </CardHeader>
      </Card>
    );
  }

  return (
    <Card section>
      <CardHeader>
        <CardTitle>Password</CardTitle>
        <CardDescription>Changing it signs you out on your other devices.</CardDescription>
      </CardHeader>
      <CardContent>
        <form
          className="flex max-w-sm flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            change.mutate();
          }}
        >
          {/* Lets password managers attach the new password to the right account. */}
          <input type="text" autoComplete="username" value={me.username} readOnly hidden />
          <Field label="Current password" htmlFor="cur">
            <PasswordInput
              id="cur"
              autoComplete="current-password"
              required
              value={current}
              onChange={(e) => setCurrent(e.target.value)}
            />
          </Field>
          <Field label="New password" htmlFor="new" hint="At least 8 characters.">
            <PasswordInput
              id="new"
              autoComplete="new-password"
              minLength={8}
              required
              value={next}
              onChange={(e) => setNext(e.target.value)}
            />
          </Field>
          <Field label="Confirm new password" htmlFor="confirm">
            <PasswordInput
              id="confirm"
              autoComplete="new-password"
              minLength={8}
              required
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
            />
          </Field>
          <ErrorNote error={change.error} />
          {change.isSuccess && <p className="text-sm text-up">Password changed.</p>}
          <Button type="submit" className="w-fit" disabled={change.isPending}>
            Change password
          </Button>
        </form>
      </CardContent>
    </Card>
  );
}

function DevicesCard() {
  const me = useMe();
  const qc = useQueryClient();
  const sessions = useQuery({ queryKey: ["sessions"], queryFn: api.sessions });
  const signOutOthers = useMutation({
    mutationFn: api.signOutOthers,
    onSuccess: (s) => qc.setQueryData(["sessions"], s),
  });
  const others = Math.max(0, (sessions.data?.count ?? 1) - 1);

  return (
    <Card section>
      <CardHeader>
        <CardTitle>Devices</CardTitle>
        <CardDescription>
          {me.last_login_at ? `Last signed in ${timeAgo(me.last_login_at)}. ` : ""}
          {sessions.data &&
            (others === 0
              ? "You're only signed in on this device."
              : `You're also signed in on ${others} other ${others === 1 ? "device" : "devices"}.`)}
        </CardDescription>
      </CardHeader>
      {others > 0 && (
        <CardContent className="flex flex-col gap-3">
          <Button
            variant="outline"
            className="w-fit"
            onClick={() => signOutOthers.mutate()}
            disabled={signOutOthers.isPending}
          >
            Sign out other devices
          </Button>
          <ErrorNote error={signOutOthers.error} />
        </CardContent>
      )}
    </Card>
  );
}
