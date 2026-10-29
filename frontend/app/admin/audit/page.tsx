"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { Spinner } from "@/components/ui";

// The audit log lives in the Logs tab of the admin page.
export default function AuditRedirect() {
  const router = useRouter();
  useEffect(() => {
    router.replace("/admin?tab=logs");
  }, [router]);
  return <Spinner />;
}
