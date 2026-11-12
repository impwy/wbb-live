// Cleanup is part of the verdict, including operations created by failed tests.
export async function allowOwned(owned,registeredPaths,id,path,send){
  // The fixture can commit before its HTTP response is observed. From this
  // point on, even a rejected/timeout response needs close and revoke.
  owned.add(id);
  registeredPaths.set(id,path);
  return send();
}

export async function cleanupBoundary({owned,registeredPaths,armed,pendingPosts,close,release,waitCompleted,revoke,assertAbsent}) {
  const failures=[];
  const attempt=async(label,action)=>{
    try{await action()}
    catch(error){failures.push(`${label}: ${error.message}`)}
  };
  for(const id of owned)await attempt(`close ${id}`,()=>close(id));
  for(const [stage,id] of [...armed.values()])await attempt(`release ${stage}/${id}`,()=>release(stage,id));
  await Promise.allSettled(pendingPosts);
  for(const id of owned)await attempt(`completed ${id}`,()=>waitCompleted(id));
  for(const [id,path] of registeredPaths)await attempt(`revoke ${id}`,()=>revoke(id,path));
  await attempt('owned sessions and paths absent',assertAbsent);
  if(failures.length)throw new Error(`cleanup failed: ${failures.join('; ')}`);
}
