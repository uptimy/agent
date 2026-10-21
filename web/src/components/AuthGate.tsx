import { useState } from "react";
import { useNavigate } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError, api, type AuthState, type User } from "@/lib/api";
import { useLiveUpdates } from "@/lib/live";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Field, Input, PasswordInput } from "@/components/ui/input";
import { ErrorNote, Logo } from "./Layout";

function useAuth() {
  return useQuery({ queryKey: ["auth"], queryFn: api.authState, retry: false });
}

/** The signed-in user. Only use inside <AuthGate>. */
export function useMe(): User {
  return useAuth().data!.user!;
}

/** Whether the signed-in user can change things (admins) or only look (viewers). */
export function useCanEdit(): boolean {
  return useMe().role === "admin";
}

const signedOut: AuthState = { authenticated: false, user: null };

/**
 * Signs out and shows the login form. The auth state is set to "signed out"
 * rather than cleared: clearing it left the gate waiting for data that never
 * came, which rendered a blank page. Everything else cached is dropped so the
 * next person to sign in on this browser doesn't see the previous user's data.
 */
export function useSignOut() {
  const qc = useQueryClient();
  const navigate = useNavigate();
  return useMutation({
    mutationFn: api.logout,
    onSettled: () => {
      qc.setQueryData(["auth"], signedOut);
      qc.removeQueries({ predicate: (q) => q.queryKey[0] !== "auth" });
      navigate("/", { replace: true });
    },
  });
}

export function AuthGate({ children }: { children: React.ReactNode }) {
  const state = useAuth();
  const user = state.data?.user;
  useLiveUpdates(!!user && !user.must_change_password);

  if (state.isPending) return null;
  if (state.error) {
    return (
      <Centered>
        <ErrorNote error={state.error} />
      </Centered>
    );
  }
  if (!user) return <LoginForm />;
  if (user.must_change_password) return <ChangePasswordForm user={user} />;
  return <>{children}</>;
}

function Centered({ children }: { children: React.ReactNode }) {
  return (
    <div className="grid min-h-dvh place-items-center p-4">
      <div className="w-full max-w-sm">
        <div className="mb-6 flex justify-center">
          <Logo />
        </div>
        {children}
      </div>
    </div>
  );
}

function useSetAuth() {
  const qc = useQueryClient();
  return (s: AuthState) => {
    qc.setQueryData(["auth"], s);
    qc.invalidateQueries({ predicate: (q) => q.queryKey[0] !== "auth" });
  };
}

function LoginForm() {
  const setAuth = useSetAuth();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  // Accounts with two-factor sign-in answer a correct password with a
  // request for the code, so the form moves to a second step.
  const [needsCode, setNeedsCode] = useState(false);
  const login = useMutation({
    mutationFn: () => api.login(username, password, needsCode ? code : undefined),
    onSuccess: setAuth,
    onError: (e) => {
      if (e instanceof ApiError && e.body.two_factor_required) setNeedsCode(true);
    },
  });
  // Asking for the code isn't an error worth showing; a wrong code is.
  const error = needsCode && !code ? null : login.error;

  return (
    <Centered>
      <Card section>
        <CardHeader>
          <CardTitle>{needsCode ? "Two-factor sign-in" : "Sign in"}</CardTitle>
          <CardDescription>
            {needsCode ? (
              <>Enter the 6-digit code from your authenticator app, or one of your recovery codes.</>
            ) : (
              <>
                First time? Sign in as <b>admin</b> with the password from <code>ADMIN_PASSWORD</code>, or the one
                printed in the agent's startup log.
              </>
            )}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form
            className="flex flex-col gap-4"
            onSubmit={(e) => {
              e.preventDefault();
              login.mutate();
            }}
          >
            {needsCode ? (
              <Field label="Code" htmlFor="code">
                <Input
                  id="code"
                  autoComplete="one-time-code"
                  autoCapitalize="none"
                  spellCheck={false}
                  required
                  autoFocus
                  placeholder="123456"
                  className="font-mono tracking-widest"
                  value={code}
                  onChange={(e) => setCode(e.target.value)}
                />
              </Field>
            ) : (
              <>
                <Field label="Username" htmlFor="username">
                  <Input
                    id="username"
                    autoComplete="username"
                    autoCapitalize="none"
                    spellCheck={false}
                    required
                    autoFocus
                    value={username}
                    onChange={(e) => setUsername(e.target.value)}
                  />
                </Field>
                <Field label="Password" htmlFor="password">
                  <PasswordInput
                    id="password"
                    autoComplete="current-password"
                    required
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                  />
                </Field>
              </>
            )}
            <ErrorNote error={error} />
            <Button type="submit" disabled={login.isPending}>
              {needsCode ? "Verify" : "Sign in"}
            </Button>
            {needsCode && (
              <button
                type="button"
                className="text-sm text-muted-foreground hover:text-foreground"
                onClick={() => {
                  setNeedsCode(false);
                  setCode("");
                  login.reset();
                }}
              >
                Back
              </button>
            )}
          </form>
        </CardContent>
      </Card>
    </Centered>
  );
}

function ChangePasswordForm({ user }: { user: User }) {
  const setAuth = useSetAuth();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const change = useMutation({
    mutationFn: async () => {
      if (next !== confirm) throw new Error("The new passwords don't match");
      return api.changePassword(current, next);
    },
    onSuccess: setAuth,
  });
  const logout = useSignOut();

  return (
    <Centered>
      <Card section>
        <CardHeader>
          <CardTitle>Choose your password</CardTitle>
          <CardDescription>
            Signed in as <b>{user.username}</b> with a temporary password. Pick your own to continue.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form
            className="flex flex-col gap-4"
            onSubmit={(e) => {
              e.preventDefault();
              change.mutate();
            }}
          >
            {/* Lets password managers attach the new password to the right account. */}
            <input type="text" autoComplete="username" value={user.username} readOnly hidden />
            <Field label="Temporary password" htmlFor="current">
              <PasswordInput
                id="current"
                autoComplete="current-password"
                required
                autoFocus
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
            <Button type="submit" disabled={change.isPending}>
              Save and continue
            </Button>
            <Button variant="ghost" onClick={() => logout.mutate()}>
              Sign out
            </Button>
          </form>
        </CardContent>
      </Card>
    </Centered>
  );
}
