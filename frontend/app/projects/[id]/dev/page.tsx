"use client";

import { useParams } from "next/navigation";
import Shell from "@/components/Shell";
import ProjectNav from "@/components/ProjectNav";
import CredentialsPanel from "@/components/CredentialsPanel";
import { useProject } from "@/lib/project";
import { ErrorBox, Spinner } from "@/components/ui";

export default function ProjectDevPage() {
  const params = useParams<{ id: string }>();
  const { project, pendingRelay, error } = useProject(params.id);
  if (error) return <Shell><div className="p-6"><ErrorBox error={error} /></div></Shell>;
  if (!project) return <Shell><Spinner /></Shell>;
  return (
    <Shell>
      <div className="flex h-full flex-col">
        <ProjectNav project={project} pendingRelay={pendingRelay} />
        <div className="min-h-0 flex-1 scroll">
          <div className="mx-auto max-w-5xl p-6">
            <CredentialsPanel project={project} />
          </div>
        </div>
      </div>
    </Shell>
  );
}
