from typing import List, Optional
from pydantic import BaseModel


class FilePayload(BaseModel):
    name: str
    type: str
    data: Optional[str] = "" 
    hash: Optional[str] = ""


class Message(BaseModel):
    sender: str
    receiver: str
    message_type: str
    message: str = ""          
    payload: List[FilePayload] 
