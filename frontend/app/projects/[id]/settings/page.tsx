"use client";

import { useParams, useRouter } from "next/navigation";
import { useEffect } from "react";
import { Spinner } from "@/components/ui";

// Old URL kept for bookmarks: project management moved to /manage.
export default function SettingsRedirect() {
  const params = useParams<{ id: string }>();
  const router = useRouter();
  useEffect(() => {
    router.replace(`/projects/${params.id}/manage`);
  }, [params.id, router]);
  return <Spinner />;
}
