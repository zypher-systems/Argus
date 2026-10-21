import { createContext, useContext, type ReactNode } from "react";
import type { AppUser } from "./types";

interface AuthCtx {
  user: AppUser | null;
}

const Ctx = createContext<AuthCtx>({ user: null });

export function AuthProvider({ user, children }: { user: AppUser | null; children: ReactNode }) {
  return <Ctx.Provider value={{ user }}>{children}</Ctx.Provider>;
}

export function useAuth() {
  return useContext(Ctx);
}

export function canWrite(user: AppUser | null): boolean {
  return !!user && user.role !== "viewer";
}

export function isAdmin(user: AppUser | null): boolean {
  return user?.role === "admin";
}
