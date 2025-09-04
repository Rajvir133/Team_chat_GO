from pydantic import BaseModel
from typing import List, Dict, Union

class Message(BaseModel):
    sender: str
    receiver: str
    message_type: str
    message: str = ""  # New field for text/caption
    payload: Union[str, List[Dict[str, str]]]